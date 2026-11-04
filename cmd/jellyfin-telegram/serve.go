package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Aryan-mor/jellyfin-telegram/internal/autoupload"
	"github.com/Aryan-mor/jellyfin-telegram/internal/blockstore"
	"github.com/Aryan-mor/jellyfin-telegram/internal/catalog"
	"github.com/Aryan-mor/jellyfin-telegram/internal/config"
	"github.com/Aryan-mor/jellyfin-telegram/internal/downloads"
	"github.com/Aryan-mor/jellyfin-telegram/internal/fladder"
	"github.com/Aryan-mor/jellyfin-telegram/internal/front"
	"github.com/Aryan-mor/jellyfin-telegram/internal/fuckjoin"
	"github.com/Aryan-mor/jellyfin-telegram/internal/gateway"
	"github.com/Aryan-mor/jellyfin-telegram/internal/indexer"
	"github.com/Aryan-mor/jellyfin-telegram/internal/jellyfin"
	"github.com/Aryan-mor/jellyfin-telegram/internal/lang"
	"github.com/Aryan-mor/jellyfin-telegram/internal/library"
	"github.com/Aryan-mor/jellyfin-telegram/internal/localmedia"
	"github.com/Aryan-mor/jellyfin-telegram/internal/managed"
	"github.com/Aryan-mor/jellyfin-telegram/internal/media"
	"github.com/Aryan-mor/jellyfin-telegram/internal/newsfeed"
	"github.com/Aryan-mor/jellyfin-telegram/internal/panel"
	"github.com/Aryan-mor/jellyfin-telegram/internal/seerr"
	"github.com/Aryan-mor/jellyfin-telegram/internal/setup"
	"github.com/Aryan-mor/jellyfin-telegram/internal/stack"
	"github.com/Aryan-mor/jellyfin-telegram/internal/subarchive"
	"github.com/Aryan-mor/jellyfin-telegram/internal/subtitles"
	"github.com/Aryan-mor/jellyfin-telegram/internal/tgpool"
	"github.com/Aryan-mor/jellyfin-telegram/internal/tgsource"
	"github.com/Aryan-mor/jellyfin-telegram/internal/tguser"
)

const (
	reconcileEvery = time.Hour
	startupTimeout = 2 * time.Minute
	archiveIdle    = time.Minute
	// managedPort is where a private Jellyfin listens, on loopback only.
	managedPort         = 18096
	managedStartTimeout = 5 * time.Minute
)

// serve is the whole program: one port that first shows the setup page if there are no settings,
// then prepares everything, then serves Jellyfin and the streams.
func serve(ctx context.Context, cfg config.Config, envPath string, log *slog.Logger) error {
	wizard, err := setup.New(envPath, verifyTelegram(cfg.DataDir, log))
	if err != nil {
		return err
	}
	var entry front.Switch
	entry.Set(wizard.Waiting())
	srv := &http.Server{Addr: cfg.GatewayListen, Handler: &entry, ReadHeaderTimeout: 15 * time.Second}
	srvErr := make(chan error, 1)
	go func() { srvErr <- srv.ListenAndServe() }()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	if cfg.NeedsSetup() {
		address := fmt.Sprintf("%s/setup?code=%s", cfg.LoopbackURL(), wizard.Code())
		fmt.Printf("\nOpen this address to set up jellyfin-telegram:\n\n    %s\n\n", address)
		openBrowser(address)
		select {
		case <-wizard.Saved():
		case err := <-srvErr:
			return fmt.Errorf("cannot serve on %s: %w", cfg.GatewayListen, err)
		case <-ctx.Done():
			return nil
		}
		if cfg, err = config.Reload(envPath); err != nil {
			return err
		}
	}
	err = run(ctx, cfg, envPath, wizard, &entry, srvErr, log)
	if err != nil {
		wizard.SetStatus(setup.Status{Stage: "Could not start", Error: err.Error()})
	}
	return err
}

func run(ctx context.Context, cfg config.Config, envPath string, wizard *setup.Wizard, entry *front.Switch, srvErr <-chan error, log *slog.Logger) error {
	// Cancelled on every return, including a failed start. Otherwise the process exits and the
	// Jellyfin child keeps the port.
	ctx, stopChildren := context.WithCancel(ctx)
	var child sync.WaitGroup
	var serverDone chan struct{}
	armDone := func() {
		if serverDone != nil {
			return
		}
		serverDone = make(chan struct{})
		go func() {
			child.Wait()
			close(serverDone)
		}()
	}
	defer func() {
		stopChildren()
		if serverDone != nil {
			<-serverDone
		}
	}()

	if err := cfg.ValidateServe(); err != nil {
		return err
	}
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return err
	}
	token, err := cfg.EnsureGatewayToken()
	if err != nil {
		return fmt.Errorf("gateway token: %w", err)
	}

	cat, err := catalog.Open(filepath.Join(cfg.DataDir, "catalog.db"))
	if err != nil {
		return fmt.Errorf("open catalog: %w", err)
	}
	defer cat.Close()
	if err := checkChannelUnchanged(ctx, cat, cfg.ChannelID); err != nil {
		return err
	}

	store, err := blockstore.Open(filepath.Join(cfg.DataDir, "cache"), cfg.CacheBytes)
	if err != nil {
		return fmt.Errorf("open cache: %w", err)
	}

	streamBase := cfg.GatewayPublicURL
	if streamBase == "" {
		streamBase = cfg.LoopbackURL()
	}
	lib := library.New(cfg.LibraryDir, streamBase, token, cfg.ChannelID)
	if err := lib.Prepare(); err != nil {
		return fmt.Errorf("prepare library: %w", err)
	}

	wizard.SetStatus(setup.Status{Stage: "Connecting to Telegram", Percent: 2})
	pool, err := newPool(cfg, cfg.BotTokens, log)
	if err != nil {
		return err
	}
	pool.Start(ctx)
	if err := waitForBots(ctx, pool, log); err != nil {
		return err
	}

	var creds managed.Credentials
	if cfg.JellyfinManaged {
		server, c, err := startManaged(ctx, cfg, wizard, log)
		if err != nil {
			return err
		}
		creds = c
		cfg.JellyfinURL, cfg.JellyfinAPIKey = server.URL(), c.APIKey
		cfg.JellyfinDataDir = server.DataDir
		cfg.FFprobePath = server.Paths.FFprobe
		server.Prepare()
		child.Add(1)
		go func() {
			defer child.Done()
			server.Run(ctx)
		}()
		armDone()
		wizard.SetStatus(setup.Status{Stage: "Starting Jellyfin", Percent: 90})
		if err := server.WaitReady(ctx, managedStartTimeout); err != nil {
			return err
		}
		if creds, err = finishManaged(ctx, cfg, server); err != nil {
			return err
		}
		cfg.JellyfinAPIKey = creds.APIKey
	}

	var seerrTarget *url.URL
	if cfg.SeerrManaged {
		base, err := startSeerr(ctx, cfg, creds, wizard, log, &child, armDone)
		if err != nil {
			return err
		}
		seerrTarget, err = url.Parse(base)
		if err != nil {
			return err
		}
	}

	var fladderTarget *url.URL
	if cfg.FladderManaged {
		base, err := startFladder(ctx, cfg, wizard, log, &child, armDone)
		if err != nil {
			return err
		}
		fladderTarget, err = url.Parse(base)
		if err != nil {
			return err
		}
	}

	engine := gateway.NewEngine(store, pool, log)
	gw := gateway.Handler(engine, cat, gateway.Options{
		Token:     token,
		ChannelID: cfg.ChannelID,
		Readahead: cfg.ReadaheadBlocks,

		BackgroundBytesPerSecond: cfg.ExtractBytesPerSecond,
	}, log)
	users := startUsers(ctx, cfg, log)
	dl := startDownloads(cfg, envPath, cat, pool, users, seerrTarget, log)
	local := startLocal(cfg, envPath, cat, pool, users, log)
	sources := startSources(cfg, cat, users)
	news, fj := startNews(cfg, cat, users, sources, log)
	radarrURL, _ := url.Parse("http://127.0.0.1:" + strconv.Itoa(stack.RadarrPort))
	sonarrURL, _ := url.Parse("http://127.0.0.1:" + strconv.Itoa(stack.SonarrPort))
	options := front.Options{
		Gateway: gw,
		Setup:   wizard.Handler(),
		Panel: settingsPanel(envPath, cfg.DataDir, wizard.Code(), pool, cat, cfg.GatewayListen, log, seerrTarget != nil, fladderTarget != nil, dl, users, local, sources, news, fj,
			func() []panel.Account { return signInDetails(cfg, creds, seerrTarget != nil, fladderTarget != nil) }),
		StreamBase: streamBase,
		Seerr:      seerrTarget,
		Fladder:    fladderTarget,
		Radarr:     radarrURL,
		Sonarr:     sonarrURL,
	}
	if cfg.JellyfinURL != "" {
		target, err := url.Parse(cfg.JellyfinURL)
		if err != nil {
			return fmt.Errorf("JELLYFIN_URL: %w", err)
		}
		options.Jellyfin = target
	}
	entry.Set(front.Handler(options))
	if dl != nil {
		go dl.Run(ctx)
	}
	go local.Run(ctx)
	if sources != nil {
		go sources.Run(ctx)
	}
	if news != nil {
		go news.Run(ctx)
	}
	log.Info("serving", "addr", cfg.GatewayListen)

	var prober *jellyfin.Prober
	if cfg.JellyfinEnabled() {
		if !cfg.JellyfinManaged {
			publishAddress(ctx, cfg, log)
		}
		prober = jellyfin.NewProber(jellyfin.New(cfg.JellyfinURL, cfg.JellyfinAPIKey), cat, engine, log)
		prober.StreamURL = lib.StreamURL
		if err := setupSubtitles(ctx, cfg, cat, pool, prober, log); err != nil {
			return err
		}
	} else {
		log.Warn("no Jellyfin is configured, so new files are not probed automatically")
	}

	addresses := reachableAddresses(cfg.GatewayListen)
	wizard.SetStatus(setup.Status{Ready: true, URLs: addresses, User: creds.User, Password: creds.Password, Seerr: seerrTarget != nil, Fladder: fladderTarget != nil})
	fmt.Printf("\nJellyfin is ready at:\n")
	for _, a := range addresses {
		fmt.Printf("    %s\n", a)
	}
	if creds.Password != "" {
		fmt.Printf("\nSign in with user %q. The password is in %s\n", creds.User, managedCredentialsFile(cfg))
	}
	if seerrTarget != nil {
		fmt.Printf("\nSeerr is at the same addresses, under /seerr/\n")
		fmt.Printf("Sign in there with the same Jellyfin user.\n")
	}
	if fladderTarget != nil {
		fmt.Printf("\nFladder is at the same addresses, under /fladder/\n")
		if seerrTarget != nil {
			fmt.Printf("Seerr is already filled in.\n")
		}
	}
	fmt.Printf("\nSettings panel:\n    %s/panel\n\n", cfg.LoopbackURL())

	idx := indexer.New(pool, cat, lib, store, log)
	if cfg.JellyfinEnabled() {
		idx.SetIdentifier(jellyfin.Identifier{Client: jellyfin.New(cfg.JellyfinURL, cfg.JellyfinAPIKey)})
	}
	loopDone := make(chan struct{})
	go func() {
		defer close(loopDone)
		syncLoop(ctx, cfg, idx, prober, pool, cat, log)
	}()
	archiveDone := make(chan struct{})
	go func() {
		defer close(archiveDone)
		if prober != nil {
			archiveLoop(ctx, cfg, cat, prober, pool, lib, log)
		}
	}()

	var result error
	select {
	case err := <-srvErr:
		result = fmt.Errorf("cannot serve on %s: %w", cfg.GatewayListen, err)
	case <-ctx.Done():
	}
	log.Info("shutting down")
	if result == nil {
		<-loopDone
		<-archiveDone
		if serverDone != nil {
			<-serverDone
		}
	}
	return result
}

func uploadPace(cfg config.Config) tguser.Pace {
	return tguser.Pace{
		MBPerSec: cfg.UploadMBPerSec, RestMinutes: cfg.UploadRestMinutes,
		LongEveryGB: cfg.UploadLongRestEveryGB, LongRestMinutes: cfg.UploadLongRestMinutes,
		DailyGB: cfg.UploadDailyGB, Hours: cfg.UploadHours,
	}
}

// startUsers connects the Telegram accounts a person signed in to. Downloads and the local files tab
// share them, so one account never sends two files at once. It returns nil when they cannot start.
func startUsers(ctx context.Context, cfg config.Config, log *slog.Logger) *tguser.Manager {
	users, err := tguser.New(filepath.Join(cfg.DataDir, "users"), cfg.APIID, cfg.APIHash, cfg.ChannelID, uploadPace(cfg), log)
	if err != nil {
		log.Warn("user sessions are off", "err", err)
		return nil
	}
	users.Bind(ctx)
	if err := users.Connect(ctx); err != nil {
		log.Warn("user sessions did not start", "err", err)
	}
	return users
}

// startLocal lists the videos already on this computer. It never starts an upload on its own.
func startLocal(cfg config.Config, envPath string, cat *catalog.Catalog, pool *tgpool.Pool, users *tguser.Manager, log *slog.Logger) *localmedia.Service {
	svc := localmedia.New(cfg.LocalMediaDirs)
	svc.Cat, svc.EnvPath, svc.Log = cat, envPath, log
	// The Telegram library of .strm files, the downloads and this program's own data are never listed.
	for _, dir := range []string{cfg.LibraryDir, cfg.DownloadsDir, cfg.DataDir} {
		if abs, err := filepath.Abs(dir); err == nil {
			svc.Exclude = append(svc.Exclude, abs)
		}
	}
	if users != nil {
		svc.Accounts = users
		svc.Worker = &autoupload.Worker{Cat: cat, Sessions: users, Verify: pool, Local: true, Log: log}
	}
	return svc
}

func startDownloads(cfg config.Config, envPath string, cat *catalog.Catalog, pool *tgpool.Pool, users *tguser.Manager, seerrTarget *url.URL, log *slog.Logger) *downloads.Service {
	if seerrTarget == nil {
		return nil // downloads are part of Seerr
	}
	pace := uploadPace(cfg)
	seerrBase, seerrKey := "", ""
	if seerrTarget != nil {
		seerrBase = seerrTarget.String()
		seerrKey, _ = seerr.EnsureAPIKey(filepath.Join(cfg.DataDir, "seerr"))
	}
	// Docker reads a relative -v path as a volume name, so both folders must be absolute.
	downloadsDir, err := filepath.Abs(cfg.DownloadsDir)
	if err != nil {
		log.Warn("downloads are off", "err", err)
		return nil
	}
	stackDir, err := filepath.Abs(filepath.Join(cfg.DataDir, "stack"))
	if err != nil {
		log.Warn("downloads are off", "err", err)
		return nil
	}
	runner := &stack.Runner{
		Downloads: downloadsDir, ConfigDir: stackDir,
		SeerrURL: seerrBase, SeerrKey: seerrKey, PublicBase: publicBase(cfg),
		MaxDBGB: cfg.BitmagnetDBMaxGB, Log: log,
	}
	svc := downloads.New(cfg.DownloadsEnabled)
	svc.EnvPath = envPath
	svc.Users = users
	svc.Runner = runner
	svc.Cat = cat
	svc.Defaults = pace
	svc.MinFree = cfg.DownloadsMinFreeGB
	svc.SearchEvery = time.Duration(cfg.DownloadsSearchEveryMinutes) * time.Minute
	svc.Log = log
	worker := &autoupload.Worker{
		Cat: cat, Verify: pool, Dir: downloadsDir,
		MinFree: uint64(cfg.DownloadsMinFreeGB) << 30, Log: log,
	}
	if users != nil {
		worker.Sessions = users
	}
	svc.Worker = worker
	return svc
}

func settingsPanel(envPath, dataDir, code string, pool *tgpool.Pool, cat *catalog.Catalog, listen string, log *slog.Logger, seerr, fladder bool, dl *downloads.Service, users *tguser.Manager, local *localmedia.Service, sources *tgsource.Service, news *newsfeed.Service, fj *fuckjoin.Runtime, accounts func() []panel.Account) http.Handler {
	return panel.New(panel.Options{
		Accounts: accounts,
		Code:     code,
		EnvPath:  envPath,
		Seerr:    seerr,
		Fladder:  fladder,
		Downloads: func(ctx context.Context) (any, error) {
			if dl == nil {
				return nil, nil
			}
			return dl.Status(ctx)
		},
		DownloadDo: downloadActions(dl, users),
		Local: func(ctx context.Context) (any, error) {
			return local.State(ctx)
		},
		LocalDo:  localActions(local),
		Sessions: sessionsView(users),
		Sources:  sourcesView(sources, news, fj, users),
		SourceDo: sourcesDo(sources, news, fj),
		Verify:   verifyTelegram(dataDir, log),
		Bots: func() []panel.LiveBot {
			out := make([]panel.LiveBot, 0)
			for _, st := range pool.Statuses() {
				msg := ""
				if st.Err != nil {
					msg = st.Err.Error()
				}
				out = append(out, panel.LiveBot{
					ID: st.ID, Name: st.Username, OK: st.Ready && st.IsAdmin, Admin: st.IsAdmin, Error: msg,
				})
			}
			return out
		},
		URLs: func() []string { return reachableAddresses(listen) },
		SameChannel: func(id int64) error {
			return checkChannelUnchanged(context.Background(), cat, id)
		},
	})
}

func downloadActions(dl *downloads.Service, users *tguser.Manager) func(*http.Request, string, map[string]any) (any, error) {
	return func(r *http.Request, action string, body map[string]any) (any, error) {
		if users == nil {
			return nil, errors.New("Telegram accounts are not available")
		}
		switch action {
		case "enable":
			if dl == nil {
				return nil, errors.New("downloads are not available")
			}
			on, _ := body["enabled"].(bool)
			if err := dl.SetEnabled(r.Context(), on); err != nil {
				return nil, err
			}
			return map[string]any{"ok": true}, nil
		case "login":
			if cancel, _ := body["cancel"].(bool); cancel {
				users.CancelLogin()
				return map[string]any{"ok": true}, nil
			}
			if phone, _ := body["phone"].(string); phone != "" {
				return users.BeginLogin(r.Context(), phone)
			}
			if pass, _ := body["password"].(string); pass != "" {
				return users.SubmitPassword(r.Context(), pass)
			}
			code, _ := body["code"].(string)
			return users.SubmitCode(r.Context(), code)
		case "logout":
			return nil, users.Logout(r.Context(), int64(number(body["user_id"])))
		case "pace":
			p := tguser.Pace{
				MBPerSec: int(number(body["mb_per_sec"])), RestMinutes: int(number(body["rest_minutes"])),
				LongEveryGB: int(number(body["long_every_gb"])), LongRestMinutes: int(number(body["long_rest_minutes"])),
				DailyGB: int(number(body["daily_gb"])),
			}
			if hours, ok := body["hours"].(string); ok {
				p.Hours = hours
			}
			if id := int64(number(body["user_id"])); id != 0 {
				return nil, users.SetPace(id, p, true)
			}
			if dl == nil {
				return nil, errors.New("downloads are not available")
			}
			return nil, dl.SetDefaults(p)
		default:
			return nil, errors.New("unknown action")
		}
	}
}

// localActions runs what the Local files tab asks for. Every upload and every delete starts from a
// button press that names the files.
func localActions(svc *localmedia.Service) func(*http.Request, string, map[string]any) (any, error) {
	return func(r *http.Request, action string, body map[string]any) (any, error) {
		switch action {
		case "scan":
			svc.Scan(r.Context())
			return nil, nil
		case "folder":
			if dir, _ := body["add"].(string); dir != "" {
				return nil, svc.AddFolder(dir)
			}
			if dir, _ := body["remove"].(string); dir != "" {
				return nil, svc.RemoveFolder(dir)
			}
			return nil, errors.New("nothing to change")
		case "upload":
			return svc.Queue(r.Context(), idList(body["ids"]))
		case "cancel":
			return svc.Unqueue(r.Context(), idList(body["ids"]))
		case "delete":
			return svc.Remove(r.Context(), idList(body["ids"]))
		case "delete_synced":
			return svc.RemoveSynced(r.Context())
		default:
			return nil, errors.New("unknown action")
		}
	}
}

func idList(v any) []int64 {
	list, _ := v.([]any)
	out := make([]int64, 0, len(list))
	for _, item := range list {
		if n := int64(number(item)); n > 0 {
			out = append(out, n)
		}
	}
	return out
}

func number(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	default:
		return 0
	}
}

func managedCredentialsFile(cfg config.Config) string {
	return filepath.Join(cfg.DataDir, "jellyfin", "credentials.json")
}

// startManaged installs Jellyfin if needed. Its setup runs later, once the server answers.
func startManaged(ctx context.Context, cfg config.Config, wizard *setup.Wizard, log *slog.Logger) (*managed.Server, managed.Credentials, error) {
	release, err := managed.ReleaseFor(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return nil, managed.Credentials{}, err
	}
	base, err := filepath.Abs(filepath.Join(cfg.DataDir, "jellyfin"))
	if err != nil {
		return nil, managed.Credentials{}, err
	}
	client := &http.Client{Timeout: 30 * time.Minute}
	paths, err := managed.Install(ctx, filepath.Join(base, "bin"), release, client, func(name string, done, total int64) {
		percent := 5
		if total > 0 {
			percent = 5 + int(80*done/total)
		}
		wizard.SetStatus(setup.Status{Stage: "Downloading " + name, Percent: percent})
	})
	if err != nil {
		return nil, managed.Credentials{}, err
	}
	return &managed.Server{Paths: paths, DataDir: filepath.Join(base, "server"), Port: managedPort, Log: log}, managed.Credentials{}, nil
}

func finishManaged(ctx context.Context, cfg config.Config, server *managed.Server) (managed.Credentials, error) {
	library, err := filepath.Abs(cfg.LibraryDir)
	if err != nil {
		return managed.Credentials{}, err
	}
	b := &managed.Bootstrap{
		URL:       server.URL(),
		StateFile: managedCredentialsFile(cfg),
		MoviesDir: filepath.Join(library, "Movies"),
		ShowsDir:  filepath.Join(library, "Shows"),
		Language:  cfg.SubtitleLanguage,
	}
	return b.Run(ctx)
}

// startSeerr pulls the pinned image, runs it, and points it at the private Jellyfin.
func startSeerr(ctx context.Context, cfg config.Config, creds managed.Credentials, wizard *setup.Wizard, log *slog.Logger, child *sync.WaitGroup, armDone func()) (string, error) {
	root, err := filepath.Abs(filepath.Join(cfg.DataDir, "seerr"))
	if err != nil {
		return "", err
	}
	key, err := seerr.EnsureAPIKey(root)
	if err != nil {
		return "", err
	}
	wizard.SetStatus(setup.Status{Stage: "Downloading Seerr", Percent: 92})
	if err := seerr.Pull(ctx, log); err != nil {
		return "", err
	}
	srv := &seerr.Server{Root: root, APIKey: key, Log: log}
	srv.Prepare(ctx)
	child.Add(1)
	go func() {
		defer child.Done()
		srv.Run(ctx)
	}()
	armDone()
	wizard.SetStatus(setup.Status{Stage: "Starting Seerr", Percent: 96})
	if err := srv.WaitReady(ctx, managedStartTimeout); err != nil {
		return "", err
	}
	wizard.SetStatus(setup.Status{Stage: "Setting up Seerr", Percent: 98})
	host, port, err := seerrJellyfin(cfg)
	if err != nil {
		return "", err
	}
	user, pass := creds.User, creds.Password
	if pass == "" {
		user, pass, err = seerr.LoginAccount(root)
		if err != nil {
			return "", err
		}
		if err := jellyfin.New(cfg.JellyfinURL, cfg.JellyfinAPIKey).EnsureAdmin(ctx, user, pass); err != nil {
			return "", fmt.Errorf("prepare the Jellyfin user for Seerr: %w", err)
		}
	}
	public := publicBase(cfg)
	setupSeerr := seerr.Setup{
		URL:               srv.URL(),
		APIKey:            key,
		JellyfinHost:      host,
		JellyfinPort:      port,
		JellyfinUser:      user,
		JellyfinPassword:  pass,
		JellyfinPublicURL: public,
		ApplicationURL:    strings.TrimRight(public, "/") + "/seerr",
		Language:          cfg.SubtitleLanguage,
	}
	// Seerr reaching Jellyfin can fail once at random right after it starts. That must not stop the player.
	err = retry(ctx, 5, 5*time.Second, log, "Seerr setup", func() error { return setupSeerr.Run(ctx) })
	if err != nil {
		return "", err
	}
	return srv.URL(), nil
}

// startFladder runs the web client. Jellyfin and Seerr addresses are the ones a browser opens.
func startFladder(ctx context.Context, cfg config.Config, wizard *setup.Wizard, log *slog.Logger, child *sync.WaitGroup, armDone func()) (string, error) {
	root, err := filepath.Abs(filepath.Join(cfg.DataDir, "fladder"))
	if err != nil {
		return "", err
	}
	wizard.SetStatus(setup.Status{Stage: "Downloading Fladder", Percent: 99})
	if err := fladder.Pull(ctx, log); err != nil {
		return "", err
	}
	public := strings.TrimRight(publicBase(cfg), "/")
	seerrURL := ""
	if cfg.SeerrManaged {
		seerrURL = public + "/seerr"
	}
	srv := &fladder.Server{Root: root, JellyfinURL: public, SeerrURL: seerrURL, Log: log}
	srv.Prepare(ctx)
	child.Add(1)
	go func() {
		defer child.Done()
		srv.Run(ctx)
	}()
	armDone()
	if err := srv.WaitReady(ctx, managedStartTimeout); err != nil {
		return "", err
	}
	return srv.URL(), nil
}

// seerrJellyfin is the address Seerr's container uses to call Jellyfin. A loopback address in
// the settings is this machine, which a container reaches under Docker's host name.
func seerrJellyfin(cfg config.Config) (string, int, error) {
	if cfg.JellyfinURL == "" {
		return seerr.JellyfinHost(runtime.GOOS), managedPort, nil
	}
	u, err := url.Parse(cfg.JellyfinURL)
	if err != nil {
		return "", 0, fmt.Errorf("JELLYFIN_URL: %w", err)
	}
	port := 8096
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil {
			return "", 0, fmt.Errorf("JELLYFIN_URL: %w", err)
		}
		port = n
	}
	host := u.Hostname()
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		host = seerr.JellyfinHost(runtime.GOOS)
	}
	return host, port, nil
}

// publicBase is an address a browser on another device can open. Seerr stores one, for the
// Play on Jellyfin link.
func publicBase(cfg config.Config) string {
	if cfg.GatewayPublicURL != "" {
		return cfg.GatewayPublicURL
	}
	for _, a := range reachableAddresses(cfg.GatewayListen) {
		if !strings.Contains(a, "://localhost") && !strings.Contains(a, "://127.") {
			return a
		}
	}
	return cfg.LoopbackURL()
}

// verifyTelegram tries the values from the setup page with real connections.
func verifyTelegram(dataDir string, log *slog.Logger) setup.Verifier {
	return func(ctx context.Context, apiID int, apiHash string, channel int64, tokens []string) (setup.Report, error) {
		pool, err := tgpool.New(tgpool.Options{
			APIID: apiID, APIHash: apiHash, ChannelID: channel, Tokens: tokens, Concurrency: 1,
			SessionDir: filepath.Join(dataDir, "sessions"), Log: log,
		})
		if err != nil {
			return setup.Report{}, err
		}
		cctx, cancel := context.WithTimeout(ctx, startupTimeout)
		defer cancel()
		pool.Start(cctx)
		pool.WaitSettled(cctx)

		var report setup.Report
		for _, st := range pool.Statuses() {
			name := "@" + st.Username
			if st.Username == "" {
				name = fmt.Sprintf("bot %d", st.ID)
			}
			switch {
			case !st.Ready:
				report.Bots = append(report.Bots, setup.BotReport{Name: name, Error: "cannot read the channel, make it an admin"})
			case !st.IsAdmin:
				report.Bots = append(report.Bots, setup.BotReport{Name: name, Error: "is not an admin of the channel"})
			default:
				report.Channel = st.ChannelTitle
				report.Bots = append(report.Bots, setup.BotReport{Name: name, OK: true})
			}
		}
		if pool.ReadyCount() == 0 {
			return report, nil
		}
		files, _, err := scanChannel(cctx, pool)
		if err != nil {
			return report, err
		}
		report.Videos = len(files)
		return report, nil
	}
}

// reachableAddresses lists how others on the network can reach this machine.
func reachableAddresses(listen string) []string {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return nil
	}
	if host != "" && host != "0.0.0.0" && host != "::" {
		return []string{"http://" + net.JoinHostPort(host, port)}
	}
	out := []string{"http://localhost:" + port}
	interfaces, err := net.Interfaces()
	if err != nil {
		return out
	}
	for _, in := range interfaces {
		if in.Flags&net.FlagUp == 0 || virtualAdapter(in.Name) {
			continue
		}
		addrs, err := in.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok && usableAddress(n.IP) {
				out = append(out, "http://"+net.JoinHostPort(n.IP.String(), port))
			}
		}
	}
	return out
}

// virtualAdapter leaves out networks that exist only inside this machine, such as those of Docker
// and WSL, because no phone or TV can reach them.
func virtualAdapter(name string) bool {
	lower := strings.ToLower(name)
	for _, prefix := range []string{"vethernet", "docker", "br-", "veth", "virbr", "wsl", "vmware", "virtualbox"} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

// usableAddress accepts home network addresses and the 100.64.0.0/10 range used by mesh VPNs such
// as Tailscale, which also work away from home.
func usableAddress(ip net.IP) bool {
	v4 := ip.To4()
	if v4 == nil {
		return false
	}
	return v4.IsPrivate() || (v4[0] == 100 && v4[1]&0xC0 == 64)
}

// openBrowser is a convenience. On a machine with no desktop it fails, and the address printed on the
// console remains.
func openBrowser(address string) {
	if os.Getenv("JFTG_NO_BROWSER") != "" {
		return
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", address)
	case "darwin":
		cmd = exec.Command("open", address)
	default:
		cmd = exec.Command("xdg-open", address)
	}
	_ = cmd.Start()
}

func setupSubtitles(ctx context.Context, cfg config.Config, cat *catalog.Catalog, pool *tgpool.Pool, prober *jellyfin.Prober, log *slog.Logger) error {
	if cfg.SubtitleLanguage == "" {
		log.Info("SUBTITLE_LANGUAGE is not set, so subtitles are not handled")
		return nil
	}
	tools, err := subtitles.FindTools(cfg.FFprobePath)
	if err != nil {
		log.Warn("subtitle checks are off", "err", err)
		return nil
	}
	changed, err := cat.SwitchSubtitleLanguage(ctx, cfg.SubtitleLanguage)
	if err != nil {
		return fmt.Errorf("subtitle language: %w", err)
	}
	if changed {
		log.Info("subtitle language changed, checking every file again", "language", cfg.SubtitleLanguage)
	}
	resolver := &subtitles.Resolver{Tools: tools, Language: cfg.SubtitleLanguage, Local: &subarchive.ChannelSubs{
		Catalog: cat, Channel: pool, Language: cfg.SubtitleLanguage,
	}}
	if cfg.SubtitlesOnline {
		if provider, ok := subtitles.NewSubPlus(cfg.SubtitleLanguage); ok {
			resolver.Provider = provider
		} else {
			log.Warn("sub-plus.ir does not offer this language, so online subtitles are off", "language", cfg.SubtitleLanguage)
		}
	}
	prober.Subtitles = resolver
	prober.SubtitleLanguage = lang.ISO3(cfg.SubtitleLanguage)
	return nil
}

// archiveLoop keeps the embedded subtitles of every video in the channel, one video at a time.
func archiveLoop(ctx context.Context, cfg config.Config, cat *catalog.Catalog, prober *jellyfin.Prober, pool *tgpool.Pool, lib *library.Library, log *slog.Logger) {
	if cfg.ExtractBytesPerSecond == 0 {
		log.Info("subtitle extraction is off")
		return
	}
	tools, err := subtitles.FindTools(cfg.FFprobePath)
	if err != nil {
		log.Warn("subtitle extraction is off", "err", err)
		return
	}
	client := jellyfin.New(cfg.JellyfinURL, cfg.JellyfinAPIKey)
	dataDir := cfg.JellyfinDataDir
	if dataDir == "" {
		if dataDir, err = client.ProgramDataPath(ctx); err != nil {
			log.Warn("subtitle extraction is off, cannot ask Jellyfin for its data folder", "err", err)
			return
		}
	}
	root := filepath.Join(dataDir, "data", "subtitles")
	if _, err := os.Stat(filepath.Dir(root)); err != nil {
		log.Warn("subtitle extraction is off, set JELLYFIN_DATA_DIR to Jellyfin's data folder", "path", dataDir)
		return
	}
	w := &subarchive.Worker{
		Catalog: cat, Client: client, Prober: prober, Channel: pool, Extractor: tools,
		Resolver: prober.Replacements(), SubtitlesRoot: root, StreamURL: lib.StreamURL, Log: log,
	}
	if err := cat.ResetExtractTries(ctx); err != nil {
		log.Warn("cannot reset subtitle extraction tries", "err", err)
	}
	verify := time.NewTicker(reconcileEvery)
	defer verify.Stop()
	idle := time.NewTicker(archiveIdle)
	defer idle.Stop()
	runVerify := func() {
		if err := w.Verify(ctx); err != nil && ctx.Err() == nil {
			log.Warn("subtitle cache check failed", "err", err)
		}
	}
	runVerify()
	for {
		for {
			did, err := w.RunOnce(ctx)
			if err != nil && ctx.Err() == nil {
				log.Warn("subtitle archive step failed", "err", err)
			}
			if !did || err != nil {
				break
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-verify.C:
			runVerify()
		case <-idle.C:
		}
	}
}

func syncLoop(ctx context.Context, cfg config.Config, idx *indexer.Indexer, prober *jellyfin.Prober, pool *tgpool.Pool, cat *catalog.Catalog, log *slog.Logger) {
	probe := func(refresh bool) {
		if prober == nil {
			return
		}
		if err := prober.Run(ctx, refresh); err != nil && ctx.Err() == nil {
			log.Warn("probing stopped early", "err", err)
		}
	}
	reconcile := func() {
		removed, err := idx.Reconcile(ctx)
		if err != nil {
			if ctx.Err() == nil {
				log.Warn("reconcile failed", "err", err)
			}
			return
		}
		if removed > 0 {
			log.Info("reconcile finished", "removed", removed)
		}
	}

	// identify looks up movies posted without a year. probe then scans the new .nfo files, and
	// refresh makes Jellyfin drop what it had kept for them.
	identify := func() []media.File {
		changed, err := idx.Identify(ctx)
		if err != nil && ctx.Err() == nil {
			log.Warn("cannot identify titles", "err", err)
		}
		return changed
	}
	refresh := func(changed []media.File) {
		if prober == nil || len(changed) == 0 {
			return
		}
		if err := prober.RefreshMetadata(ctx, changed); err != nil && ctx.Err() == nil {
			log.Warn("cannot refresh metadata", "err", err)
		}
	}

	if err := idx.Rewrite(ctx); err != nil {
		log.Error("cannot refresh .strm files", "err", err)
	}
	added, err := idx.Sync(ctx, indexer.FullWindow)
	if err != nil && ctx.Err() == nil {
		log.Warn("initial sync failed", "err", err)
	}
	log.Info("initial sync finished", "added", added)
	reconcile()
	changed := identify()
	warmUp(ctx, cat, pool, log)
	probe(true)
	refresh(changed)

	syncTick := time.NewTicker(cfg.SyncInterval)
	defer syncTick.Stop()
	reconcileTick := time.NewTicker(reconcileEvery)
	defer reconcileTick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-syncTick.C:
			added, err := idx.Sync(ctx, indexer.IncrementalWindow)
			if err != nil && ctx.Err() == nil {
				log.Warn("sync failed", "err", err)
			}
			if added > 0 {
				probe(true)
			}
		case <-reconcileTick.C:
			if err := idx.Rewrite(ctx); err != nil {
				log.Error("cannot refresh .strm files", "err", err)
			}
			reconcile()
			changed := identify()
			probe(true)
			refresh(changed)
		}
	}
}

// warmUp opens every bot's connection to the data center of the first file.
func warmUp(ctx context.Context, cat *catalog.Catalog, pool *tgpool.Pool, log *slog.Logger) {
	all, err := cat.All(ctx)
	if err != nil || len(all) == 0 {
		return
	}
	began := time.Now()
	wctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	pool.Warm(wctx, all[0].MessageID, all[0].DocID)
	log.Info("bots warmed up", "took", time.Since(began).Round(time.Millisecond))
}

func waitForBots(ctx context.Context, pool *tgpool.Pool, log *slog.Logger) error {
	wctx, cancel := context.WithTimeout(ctx, startupTimeout)
	defer cancel()
	pool.WaitSettled(wctx)
	var failures []error
	for _, st := range pool.Statuses() {
		if !st.Ready {
			failures = append(failures, fmt.Errorf("bot %d: %v", st.ID, st.Err))
		}
	}
	if pool.ReadyCount() == 0 {
		return fmt.Errorf("no bot could connect:\n%w", errors.Join(failures...))
	}
	for _, f := range failures {
		log.Warn("bot is not ready", "err", f)
	}
	log.Info("bots ready", "ready", pool.ReadyCount(), "failed", len(failures))
	return nil
}

// checkChannelUnchanged stops a changed channel id from serving another channel's
// message ids through existing .strm files.
func checkChannelUnchanged(ctx context.Context, cat *catalog.Catalog, channel int64) error {
	all, err := cat.All(ctx)
	if err != nil {
		return err
	}
	if len(all) > 0 && all[0].ChannelID != channel {
		return fmt.Errorf("the catalog belongs to channel %d, not %d: use a new DATA_DIR and LIBRARY_DIR for a different channel",
			all[0].ChannelID, channel)
	}
	return nil
}

// publishAddress tells the user's own Jellyfin to announce this service's address when clients search
// the network. Without it an app such as Android TV finds Jellyfin's own port first, bypasses this
// service, and loses its fixes. The managed server never answers such searches, so it needs nothing.
func publishAddress(ctx context.Context, cfg config.Config, log *slog.Logger) {
	u, err := url.Parse(cfg.GatewayPublicURL)
	if err != nil || u.Host == "" {
		return
	}
	if ip := net.ParseIP(u.Hostname()); (ip != nil && ip.IsLoopback()) || strings.EqualFold(u.Hostname(), "localhost") {
		return
	}
	changed, err := jellyfin.New(cfg.JellyfinURL, cfg.JellyfinAPIKey).PublishAddress(ctx, cfg.GatewayPublicURL)
	switch {
	case err != nil:
		log.Warn("could not set the address Jellyfin announces; set Published Server URIs by hand", "err", err)
	case changed:
		log.Info("Jellyfin now announces this service's address to apps that search the network", "address", cfg.GatewayPublicURL)
	}
}
