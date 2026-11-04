package stack

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Runner starts the containers and applies their settings.
type Runner struct {
	Downloads  string
	ConfigDir  string
	SeerrURL   string
	SeerrKey   string
	PublicBase string
	MaxDBGB    int
	Log        *slog.Logger

	mu     sync.Mutex
	status Status
	capMB  float64
	// configured is true once the apps were set up for configuredCap, so a later pass leaves them alone.
	configured    bool
	configuredCap float64
}

// Status is what the panel shows. Passwords are the WebUI passwords, not API keys.
type Status struct {
	Error string `json:"error,omitempty"`
	// Warning is a step that failed without stopping the stack, such as telling Seerr about Radarr.
	Warning        string      `json:"warning,omitempty"`
	Ready          bool        `json:"ready"`
	Crawler        bool        `json:"crawler"`
	FreeBytes      uint64      `json:"free_bytes"`
	RadarrUser     string      `json:"radarr_user,omitempty"`
	RadarrPassword string      `json:"radarr_password,omitempty"`
	SonarrUser     string      `json:"sonarr_user,omitempty"`
	SonarrPassword string      `json:"sonarr_password,omitempty"`
	RadarrAPIKey   string      `json:"-"`
	SonarrAPIKey   string      `json:"-"`
	QbitPassword   string      `json:"-"`
	Containers     []Container `json:"containers,omitempty"`
}

// Container is one Docker container in the stack.
type Container struct {
	Name    string `json:"name"`
	Running bool   `json:"running"`
}

type secrets struct {
	RadarrPassword   string `json:"radarr_password"`
	SonarrPassword   string `json:"sonarr_password"`
	QbitPassword     string `json:"qbit_password"`
	PostgresPassword string `json:"postgres_password"`
	RadarrAPIKey     string `json:"radarr_api_key"`
	SonarrAPIKey     string `json:"sonarr_api_key"`
}

// SetCapMB is the largest file a signed-in account can upload, in mebibytes.
func (r *Runner) SetCapMB(mb float64) {
	r.mu.Lock()
	r.capMB = mb
	r.mu.Unlock()
}

// Status returns the last known state.
func (r *Runner) Snapshot() Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.status
}

func (r *Runner) set(s Status) {
	r.mu.Lock()
	r.status = s
	r.mu.Unlock()
}

// Reconcile pulls images, starts containers and applies settings. It is safe to call again.
func (r *Runner) Reconcile(ctx context.Context) error {
	if err := PrepareDir(r.Downloads); err != nil {
		r.fail(err)
		return err
	}
	sec, err := r.loadSecrets()
	if err != nil {
		r.fail(err)
		return err
	}
	// The disk guard needs the qBittorrent password even when a later step fails.
	r.mu.Lock()
	r.status.QbitPassword = sec.QbitPassword
	r.mu.Unlock()
	if err := WriteQbitConfig(filepath.Join(r.ConfigDir, "qbittorrent"), "admin", sec.QbitPassword); err != nil {
		r.fail(err)
		return err
	}
	if err := ensureNetwork(ctx); err != nil {
		r.fail(err)
		return err
	}
	for _, image := range Images() {
		if err := pull(ctx, image); err != nil {
			r.fail(err)
			return err
		}
	}
	if err := r.startAll(ctx, sec); err != nil {
		r.fail(err)
		return err
	}
	// Seerr loses the network whenever its container is made again, so this runs on every pass.
	linkErr := r.linkSeerr(ctx)
	r.mu.Lock()
	skip := r.configured && r.configuredCap == r.capMB
	capNow := r.capMB
	r.mu.Unlock()
	if !skip {
		if err := r.configure(ctx, sec); err != nil {
			r.fail(err)
			return err
		}
		r.mu.Lock()
		// A failed Seerr update left a warning, so the next pass has to try again.
		r.configured, r.configuredCap = r.status.Warning == "", capNow
		r.mu.Unlock()
		// configure saved the API keys it read from the containers.
		if saved, err := r.loadSecrets(); err == nil {
			sec = saved
		}
	}
	containers := r.containers(ctx)
	r.mu.Lock()
	r.status.Error = ""
	if linkErr != nil {
		r.status.Warning = "Seerr cannot reach Radarr and Sonarr: " + linkErr.Error()
	}
	r.status.Ready = true
	r.status.RadarrUser = "admin"
	r.status.RadarrPassword = sec.RadarrPassword
	r.status.SonarrUser = "admin"
	r.status.SonarrPassword = sec.SonarrPassword
	r.status.RadarrAPIKey = sec.RadarrAPIKey
	r.status.SonarrAPIKey = sec.SonarrAPIKey
	r.status.QbitPassword = sec.QbitPassword
	r.status.Containers = containers
	r.mu.Unlock()
	return nil
}

// Stop removes the containers and leaves the volumes and downloaded files in place.
func (r *Runner) Stop(ctx context.Context) {
	removeContainers(ctx)
	r.mu.Lock()
	r.status.Ready = false
	r.status.Containers = nil
	r.configured = false
	r.mu.Unlock()
}

func (r *Runner) fail(err error) {
	r.mu.Lock()
	r.status.Error = err.Error()
	r.status.Ready = false
	r.mu.Unlock()
	if r.Log != nil {
		r.Log.Warn("download stack", "err", err)
	}
}

func (r *Runner) startAll(ctx context.Context, sec secrets) error {
	if err := ensureContainer(ctx, PostgresName, postgresArgs(sec.PostgresPassword)); err != nil {
		return err
	}
	crawler := r.wantCrawler(ctx)
	if containerExists(ctx, BitmagnetName) && crawler != commandCrawls(ctx) {
		_, _ = docker(ctx, "rm", "-f", BitmagnetName)
	}
	if err := ensureContainer(ctx, BitmagnetName, bitmagnetArgs(sec.PostgresPassword, crawler)); err != nil {
		return err
	}
	r.mu.Lock()
	r.status.Crawler = crawler
	r.mu.Unlock()
	if err := ensureContainer(ctx, QbitName, qbitArgs(r.Downloads, filepath.Join(r.ConfigDir, "qbittorrent"))); err != nil {
		return err
	}
	if err := ensureContainer(ctx, RadarrName, servarrArgs("radarr", RadarrPort, RadarrInnerPort, "/radarr", VolumeRadarr, r.Downloads, sec)); err != nil {
		return err
	}
	return ensureContainer(ctx, SonarrName, servarrArgs("sonarr", SonarrPort, SonarrInnerPort, "/sonarr", VolumeSonarr, r.Downloads, sec))
}

func (r *Runner) configure(ctx context.Context, sec secrets) error {
	waitCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	radarrKey, err := waitAPIKey(waitCtx, RadarrName)
	if err != nil {
		return fmt.Errorf("radarr: %w", err)
	}
	sonarrKey, err := waitAPIKey(waitCtx, SonarrName)
	if err != nil {
		return fmt.Errorf("sonarr: %w", err)
	}
	sec.RadarrAPIKey, sec.SonarrAPIKey = radarrKey, sonarrKey
	if err := r.saveSecrets(sec); err != nil {
		return err
	}
	q := &Qbit{Base: "http://127.0.0.1:" + strconv.Itoa(QbitPort), User: "admin", Pass: sec.QbitPassword}
	if err := wait(waitCtx, q.Login); err != nil {
		return fmt.Errorf("qBittorrent: %w", err)
	}
	if err := q.ApplyLimits(ctx); err != nil {
		return err
	}
	if err := q.EnsureCategory(ctx, "radarr", "/data/torrents/movies"); err != nil {
		return err
	}
	if err := q.EnsureCategory(ctx, "sonarr", "/data/torrents/tv"); err != nil {
		return err
	}
	r.mu.Lock()
	capMB := r.capMB
	r.mu.Unlock()
	radarrBase, err := waitBase(waitCtx, RadarrPort, "/radarr")
	if err != nil {
		return err
	}
	sonarrBase, err := waitBase(waitCtx, SonarrPort, "/sonarr")
	if err != nil {
		return err
	}
	radarrProfile, err := (App{Kind: "radarr", Base: radarrBase, Key: radarrKey}).Configure(ctx, Setup{
		Root: "/data/media/movies", CapMB: capMB, RuntimeMin: 150, DownloadHost: QbitName, DownloadPort: 8080,
		DownloadUser: "admin", DownloadPass: sec.QbitPassword, Category: "radarr",
		IndexerURL: "http://" + BitmagnetName + ":3333/torznab",
		URLBase:    "/radarr", Username: "admin", Password: sec.RadarrPassword,
	})
	if err != nil {
		return fmt.Errorf("configure radarr: %w", err)
	}
	sonarrProfile, err := (App{Kind: "sonarr", Base: sonarrBase, Key: sonarrKey}).Configure(ctx, Setup{
		Root: "/data/media/tv", CapMB: capMB, RuntimeMin: 60, DownloadHost: QbitName, DownloadPort: 8080,
		DownloadUser: "admin", DownloadPass: sec.QbitPassword, Category: "sonarr",
		IndexerURL: "http://" + BitmagnetName + ":3333/torznab",
		URLBase:    "/sonarr", Username: "admin", Password: sec.SonarrPassword,
	})
	if err != nil {
		return fmt.Errorf("configure sonarr: %w", err)
	}
	r.mu.Lock()
	r.status.Warning = ""
	r.mu.Unlock()
	if r.SeerrURL == "" {
		return nil
	}
	// Seerr being down must not stop the guard that watches the disk.
	if err := syncSeerr(ctx, r, sec, radarrProfile, sonarrProfile); err != nil {
		r.mu.Lock()
		r.status.Warning = "Seerr was not updated: " + err.Error()
		r.configured = false
		r.mu.Unlock()
		if r.Log != nil {
			r.Log.Warn("Seerr was not told about Radarr and Sonarr", "err", err)
		}
	}
	return nil
}

func (r *Runner) wantCrawler(ctx context.Context) bool {
	if r.MaxDBGB <= 0 || !containerRunning(ctx, PostgresName) {
		return true
	}
	out, err := docker(ctx, "exec", PostgresName, "psql", "-U", "bitmagnet", "-d", "bitmagnet", "-tAc", "SELECT COALESCE(pg_database_size('bitmagnet'),0)")
	if err != nil {
		return true
	}
	n, err := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
	if err != nil {
		return true
	}
	return n < int64(r.MaxDBGB)<<30
}

func commandCrawls(ctx context.Context) bool {
	out, err := docker(ctx, "inspect", "-f", "{{json .Config.Cmd}}", BitmagnetName)
	if err != nil {
		return true
	}
	return strings.Contains(out, "dht_crawler")
}

func (r *Runner) containers(ctx context.Context) []Container {
	var out []Container
	for _, name := range []string{QbitName, RadarrName, SonarrName, BitmagnetName, PostgresName} {
		out = append(out, Container{Name: name, Running: containerRunning(ctx, name)})
	}
	return out
}

func (r *Runner) loadSecrets() (secrets, error) {
	path := filepath.Join(r.ConfigDir, "secrets.json")
	raw, err := os.ReadFile(path)
	if err == nil {
		var s secrets
		if err := json.Unmarshal(raw, &s); err != nil {
			return secrets{}, err
		}
		if s.RadarrPassword != "" && s.QbitPassword != "" && s.PostgresPassword != "" && s.SonarrPassword != "" {
			return s, nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return secrets{}, err
	}
	s := secrets{
		RadarrPassword: randomSecret(), SonarrPassword: randomSecret(),
		QbitPassword: randomSecret(), PostgresPassword: randomSecret(),
	}
	return s, r.saveSecrets(s)
}

func (r *Runner) saveSecrets(s secrets) error {
	if err := os.MkdirAll(r.ConfigDir, 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(r.ConfigDir, "secrets.json"), append(raw, '\n'), 0o600)
}

func randomSecret() string {
	buf := make([]byte, 18)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}

var reAPIKey = regexp.MustCompile(`<ApiKey>([^<]+)</ApiKey>`)

func waitAPIKey(ctx context.Context, container string) (string, error) {
	var last error
	for {
		out, err := docker(ctx, "exec", container, "cat", "/config/config.xml")
		if err == nil {
			if m := reAPIKey.FindStringSubmatch(out); m != nil && m[1] != "" {
				return m[1], nil
			}
			last = errors.New("config.xml has no API key yet")
		} else {
			last = err
		}
		select {
		case <-ctx.Done():
			if last == nil {
				last = ctx.Err()
			}
			return "", last
		case <-time.After(time.Second):
		}
	}
}

func wait(ctx context.Context, fn func(context.Context) error) error {
	var last error
	for {
		if err := fn(ctx); err == nil {
			return nil
		} else {
			last = err
		}
		select {
		case <-ctx.Done():
			return last
		case <-time.After(time.Second):
		}
	}
}

func waitBase(ctx context.Context, port int, urlBase string) (string, error) {
	return wait2(ctx, func(ctx context.Context) (string, error) {
		return ProbeBase(ctx, port, urlBase)
	})
}

func wait2(ctx context.Context, fn func(context.Context) (string, error)) (string, error) {
	var last error
	for {
		v, err := fn(ctx)
		if err == nil {
			return v, nil
		}
		last = err
		select {
		case <-ctx.Done():
			return "", last
		case <-time.After(time.Second):
		}
	}
}

func postgresArgs(password string) []string {
	return []string{
		// Docker's default 64 MB of shared memory is too little for the indexer's queries.
		"--shm-size=1g",
		"-e", "POSTGRES_USER=bitmagnet",
		"-e", "POSTGRES_PASSWORD=" + password,
		"-e", "POSTGRES_DB=bitmagnet",
		"-v", VolumePostgres + ":/var/lib/postgresql/data",
		ImagePostgres,
	}
}

func bitmagnetArgs(password string, crawler bool) []string {
	keys := []string{"worker", "run", "--keys=http_server", "--keys=queue_server"}
	if crawler {
		keys = append(keys, "--keys=dht_crawler")
	}
	args := []string{
		"-p", "127.0.0.1:" + strconv.Itoa(BitmagnetPort) + ":3333",
		"-p", strconv.Itoa(DHTPort) + ":3334/tcp",
		"-p", strconv.Itoa(DHTPort) + ":3334/udp",
		"-e", "POSTGRES_HOST=" + PostgresName,
		"-e", "POSTGRES_USER=bitmagnet",
		"-e", "POSTGRES_PASSWORD=" + password,
		"-e", "POSTGRES_NAME=bitmagnet",
	}
	args = append(args, ImageBitmagnet)
	return append(args, keys...)
}

func qbitArgs(downloads, configDir string) []string {
	uid, gid := containerIDs()
	return []string{
		"-e", "PUID=" + uid, "-e", "PGID=" + gid, "-e", "TZ=UTC", "-e", "WEBUI_PORT=8080",
		"-p", "127.0.0.1:" + strconv.Itoa(QbitPort) + ":8080",
		"-p", strconv.Itoa(PeerPort) + ":6881/tcp",
		"-p", strconv.Itoa(PeerPort) + ":6881/udp",
		"-v", bind(downloads, "/data"),
		"-v", bind(configDir, "/config"),
		ImageQbit,
	}
}

func servarrArgs(kind string, hostPort, containerPort int, urlBase, volume, downloads string, sec secrets) []string {
	prefix := "RADARR"
	password := sec.RadarrPassword
	if kind == "sonarr" {
		prefix = "SONARR"
		password = sec.SonarrPassword
	}
	uid, gid := containerIDs()
	return []string{
		"-e", "PUID=" + uid, "-e", "PGID=" + gid, "-e", "TZ=UTC",
		"-e", prefix + "__SERVER__PORT=" + strconv.Itoa(containerPort),
		"-e", prefix + "__SERVER__URLBASE=" + urlBase,
		"-e", prefix + "__AUTH__METHOD=Forms",
		"-e", prefix + "__AUTH__REQUIRED=Enabled",
		"-e", prefix + "__AUTH__USERNAME=admin",
		"-e", prefix + "__AUTH__PASSWORD=" + password,
		"-p", fmt.Sprintf("127.0.0.1:%d:%d", hostPort, containerPort),
		"-v", bind(downloads, "/data"),
		"-v", volume + ":/config",
		imageFor(kind),
	}
}

func imageFor(kind string) string {
	if kind == "sonarr" {
		return ImageSonarr
	}
	return ImageRadarr
}

// containerIDs is the user the linuxserver images run as. On Linux it is the user running this
// program, so downloaded files can be read and deleted here. Windows has no such ids.
func containerIDs() (string, string) {
	uid, gid := os.Getuid(), os.Getgid()
	if uid <= 0 || gid <= 0 {
		return "1000", "1000"
	}
	return strconv.Itoa(uid), strconv.Itoa(gid)
}
