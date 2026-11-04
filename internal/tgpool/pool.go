// Package tgpool spreads Telegram reads over a pool of bots.
//
// Every 1 MiB block is scheduled on its own, so adding bots adds parallel download
// capacity to a single stream. A bot in flood wait is benched and others carry on.
package tgpool

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/gotd/td/tgerr"

	"github.com/Aryan-mor/jellyfin-telegram/internal/media"
)

const (
	maxAttempts      = 6
	accessBenchTime  = 10 * time.Minute
	acquirePollDelay = 25 * time.Millisecond
	floodMargin      = time.Second
	warmBytes        = 4096
)

// ErrMessageGone means the message no longer holds the expected document.
var ErrMessageGone = errors.New("message no longer holds this document")

// BotStatus describes one bot for the check command and for logs.
type BotStatus struct {
	ID           int64
	Username     string
	Ready        bool
	Err          error
	ChannelTitle string
	IsAdmin      bool
}

// worker is one bot connection. The interface lets tests replace Telegram.
type worker interface {
	settled() <-chan struct{}
	status() BotStatus
	getBlock(ctx context.Context, messageID int, docID, offset int64, limit int) ([]byte, error)
	messages(ctx context.Context, ids []int) ([]media.Message, error)
	thumbnail(ctx context.Context, messageID int) ([]byte, error)
	invalidate(messageID int)
	sendDocument(ctx context.Context, name, caption string, data []byte, replyTo int) (int, error)
	deleteMessages(ctx context.Context, ids []int) error
}

type slot struct {
	w          worker
	inflight   int
	floodUntil time.Time
	benchUntil time.Time
	calls      int64
	floods     int64
}

// BotStats counts the work done by one bot.
type BotStats struct {
	ID     int64
	Calls  int64
	Floods int64
}

// Pool schedules calls across bots.
type Pool struct {
	mu          sync.Mutex
	slots       []*slot
	concurrency int
	next        int
	log         *slog.Logger
	start       func(context.Context)
}

func newPool(workers []worker, concurrency int, log *slog.Logger) *Pool {
	p := &Pool{concurrency: concurrency, log: log}
	for _, w := range workers {
		p.slots = append(p.slots, &slot{w: w})
	}
	return p
}

// Start connects the bots. It returns at once.
func (p *Pool) Start(ctx context.Context) {
	if p.start != nil {
		p.start(ctx)
	}
}

// WaitSettled waits until every bot has connected or failed, or ctx ends.
func (p *Pool) WaitSettled(ctx context.Context) {
	for _, s := range p.slots {
		select {
		case <-s.w.settled():
		case <-ctx.Done():
			return
		}
	}
}

// Statuses returns the state of every bot.
func (p *Pool) Statuses() []BotStatus {
	out := make([]BotStatus, 0, len(p.slots))
	for _, s := range p.slots {
		out = append(out, s.w.status())
	}
	return out
}

// Stats returns per-bot counters.
func (p *Pool) Stats() []BotStats {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]BotStats, 0, len(p.slots))
	for _, s := range p.slots {
		out = append(out, BotStats{ID: s.w.status().ID, Calls: s.calls, Floods: s.floods})
	}
	return out
}

// ReadyCount returns how many bots can serve requests.
func (p *Pool) ReadyCount() int {
	n := 0
	for _, s := range p.slots {
		if s.w.status().Ready {
			n++
		}
	}
	return n
}

// FetchBlock returns one block of a document. Only the last block may be short.
func (p *Pool) FetchBlock(ctx context.Context, messageID int, docID, block, size int64) ([]byte, error) {
	offset := block * media.BlockSize
	want := min(media.BlockSize, size-offset)
	if want <= 0 {
		return nil, fmt.Errorf("block %d is past the end of a %d byte file", block, size)
	}
	buf := make([]byte, 0, want)
	for int64(len(buf)) < want {
		off := offset + int64(len(buf))
		limit := int(media.BlockSize - off%media.BlockSize)
		var data []byte
		err := p.call(ctx, messageID, func(w worker) error {
			var err error
			data, err = w.getBlock(ctx, messageID, docID, off, limit)
			return err
		})
		if err != nil {
			return nil, err
		}
		if len(data) == 0 {
			return nil, fmt.Errorf("telegram returned no data at offset %d of message %d", off, messageID)
		}
		buf = append(buf, data...)
	}
	return buf[:want], nil
}

// inChannel reads a channel other than the one the bots were started for.
type inChannel interface {
	messagesIn(ctx context.Context, channelID int64, ids []int) ([]media.Message, error)
	getBlockIn(ctx context.Context, channelID int64, messageID int, docID, offset int64, limit int) ([]byte, error)
}

// FetchIn returns one block from channelID. Channel 0 is the main channel.
func (p *Pool) FetchIn(ctx context.Context, channelID int64, messageID int, docID, block, size int64) ([]byte, error) {
	offset := block * media.BlockSize
	want := min(media.BlockSize, size-offset)
	if want <= 0 {
		return nil, fmt.Errorf("block %d is past the end of a %d byte file", block, size)
	}
	buf := make([]byte, 0, want)
	for int64(len(buf)) < want {
		off := offset + int64(len(buf))
		limit := int(media.BlockSize - off%media.BlockSize)
		var data []byte
		err := p.call(ctx, messageID, func(w worker) error {
			in, ok := w.(inChannel)
			if !ok {
				return errors.New("this bot cannot read another channel")
			}
			var err error
			data, err = in.getBlockIn(ctx, channelID, messageID, docID, off, limit)
			return err
		})
		if err != nil {
			return nil, err
		}
		if len(data) == 0 {
			return nil, fmt.Errorf("telegram returned no data at offset %d of message %d", off, messageID)
		}
		buf = append(buf, data...)
	}
	return buf[:want], nil
}

// MessagesIn looks up message ids in channelID. Videos and photos sent as files are returned.
func (p *Pool) MessagesIn(ctx context.Context, channelID int64, ids []int) ([]media.Message, error) {
	var out []media.Message
	err := p.call(ctx, 0, func(w worker) error {
		in, ok := w.(inChannel)
		if !ok {
			return errors.New("this bot cannot read another channel")
		}
		var err error
		out, err = in.messagesIn(ctx, channelID, ids)
		return err
	})
	return out, err
}

// Messages looks up message ids in the channel.
func (p *Pool) Messages(ctx context.Context, ids []int) ([]media.Message, error) {
	var out []media.Message
	err := p.call(ctx, 0, func(w worker) error {
		var err error
		out, err = w.messages(ctx, ids)
		return err
	})
	return out, err
}

// Download returns a whole document. It is meant for small files such as subtitle archives.
func (p *Pool) Download(ctx context.Context, messageID int, docID, size int64) ([]byte, error) {
	out := make([]byte, 0, size)
	for block := int64(0); block*media.BlockSize < size; block++ {
		data, err := p.FetchBlock(ctx, messageID, docID, block, size)
		if err != nil {
			return nil, err
		}
		out = append(out, data...)
	}
	return out, nil
}

// SendDocument posts a file to the channel and returns the new message id.
func (p *Pool) SendDocument(ctx context.Context, name, caption string, data []byte, replyTo int) (int, error) {
	var id int
	err := p.call(ctx, 0, func(w worker) error {
		var err error
		id, err = w.sendDocument(ctx, name, caption, data, replyTo)
		return err
	})
	return id, err
}

// DeleteMessages removes messages from the channel.
func (p *Pool) DeleteMessages(ctx context.Context, ids []int) error {
	return p.call(ctx, 0, func(w worker) error { return w.deleteMessages(ctx, ids) })
}

// AcceptMirror checks that botID can post in the named channel and is an admin of the main
// channel. It returns the channel's bare id and title.
func (p *Pool) AcceptMirror(ctx context.Context, botID, channelID int64, username string) (int64, string, error) {
	b, err := p.findBot(botID)
	if err != nil {
		return 0, "", err
	}
	ch, err := b.openChannel(ctx, channelID, username)
	if err != nil {
		return 0, "", err
	}
	title := ch.Title
	if title == "" {
		title = fmt.Sprintf("Channel %d", ch.ID)
	}
	return ch.ID, title, nil
}

// CopyMessage forwards one message from the main channel into channelID with that bot.
// The author line is dropped. The returned id is zero when Telegram does not report one.
func (p *Pool) CopyMessage(ctx context.Context, botID int64, messageID int, channelID int64) (int, error) {
	b, err := p.findBot(botID)
	if err != nil {
		return 0, err
	}
	ch, err := b.openChannel(ctx, channelID, "")
	if err != nil {
		return 0, err
	}
	return b.forwardTo(ctx, messageID, ch)
}

func (p *Pool) findBot(id int64) (*bot, error) {
	for _, s := range p.slots {
		b, ok := s.w.(*bot)
		if ok && b.id == id {
			return b, nil
		}
	}
	return nil, errors.New("that bot is not listed")
}

// Warm makes every bot read a little of one file. The first read of a bot has to open a connection
// to the file's data center, which takes seconds, so this keeps that wait away from playback.
func (p *Pool) Warm(ctx context.Context, messageID int, docID int64) {
	var wg sync.WaitGroup
	for _, s := range p.slots {
		if !s.w.status().Ready {
			continue
		}
		wg.Add(1)
		go func(w worker) {
			defer wg.Done()
			if _, err := w.getBlock(ctx, messageID, docID, 0, warmBytes); err != nil {
				p.log.Debug("warm-up read failed", "bot", w.status().ID, "err", err)
			}
		}(s.w)
	}
	wg.Wait()
}

// Thumbnail returns the largest thumbnail of a document.
func (p *Pool) Thumbnail(ctx context.Context, messageID int) ([]byte, error) {
	var out []byte
	err := p.call(ctx, messageID, func(w worker) error {
		var err error
		out, err = w.thumbnail(ctx, messageID)
		return err
	})
	return out, err
}

// call runs fn on a free bot and retries on another bot when Telegram refuses.
// Flood waits do not count as attempts, so ctx must carry a deadline.
func (p *Pool) call(ctx context.Context, messageID int, fn func(worker) error) error {
	var lastErr error
	for attempt := 0; attempt < maxAttempts; {
		s, err := p.acquire(ctx)
		if err != nil {
			if lastErr != nil {
				return fmt.Errorf("%w (last error: %v)", err, lastErr)
			}
			return err
		}
		err = fn(s.w)
		p.release(s, err == nil)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(err, ErrMessageGone) {
			return err
		}
		lastErr = err

		if d, ok := tgerr.AsFloodWait(err); ok {
			p.bench(s, d+floodMargin, true)
			p.log.Warn("bot hit a flood wait", "bot", s.w.status().ID, "wait", d)
			continue
		}
		attempt++
		switch {
		case tgerr.Is(err, "FILE_REFERENCE_EXPIRED", "FILE_REFERENCE_INVALID", "FILE_ID_INVALID"):
			s.w.invalidate(messageID)
		case tgerr.Is(err, "CHANNEL_PRIVATE", "CHANNEL_INVALID", "CHAT_ADMIN_REQUIRED", "AUTH_KEY_UNREGISTERED", "USER_DEACTIVATED"):
			p.bench(s, accessBenchTime, false)
			p.log.Warn("bot lost access and is benched", "bot", s.w.status().ID, "err", err)
		default:
			select {
			case <-time.After(time.Duration(attempt) * 200 * time.Millisecond):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	return fmt.Errorf("gave up after %d attempts: %w", maxAttempts, lastErr)
}

func (p *Pool) acquire(ctx context.Context) (*slot, error) {
	for {
		if s := p.tryAcquire(time.Now()); s != nil {
			return s, nil
		}
		select {
		case <-time.After(acquirePollDelay):
		case <-ctx.Done():
			return nil, fmt.Errorf("no bot available: %w", ctx.Err())
		}
	}
}

// tryAcquire picks the ready bot with the fewest running calls, rotating between ties.
func (p *Pool) tryAcquire(now time.Time) *slot {
	p.mu.Lock()
	defer p.mu.Unlock()
	var best *slot
	bestIndex := 0
	for i := range p.slots {
		idx := (p.next + i) % len(p.slots)
		s := p.slots[idx]
		if s.inflight >= p.concurrency || now.Before(s.floodUntil) || now.Before(s.benchUntil) || !s.w.status().Ready {
			continue
		}
		if best == nil || s.inflight < best.inflight {
			best, bestIndex = s, idx
		}
	}
	if best == nil {
		return nil
	}
	best.inflight++
	p.next = (bestIndex + 1) % len(p.slots)
	return best
}

func (p *Pool) release(s *slot, ok bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	s.inflight--
	if ok {
		s.calls++
	}
}

func (p *Pool) bench(s *slot, d time.Duration, flood bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	until := time.Now().Add(d)
	if flood {
		s.floods++
		s.floodUntil = until
		return
	}
	s.benchUntil = until
}
