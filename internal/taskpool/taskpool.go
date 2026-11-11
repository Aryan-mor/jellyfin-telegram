// Package taskpool is one pool of jobs that the bots and the signed-in accounts take from when they are free.
//
// A job says who may run it: only an account, only a bot, or either. Rules for who takes what:
//
//   - An account that has a job only accounts can do takes that first, and never touches a shared job
//     while one exists. A bot does the same with the jobs only bots can do.
//   - A shared job goes to a free bot first. An account takes one only when no bot is waiting for work,
//     so the account helps and never gets in the way of the bots.
//   - While something is playing, only jobs of Requested priority or higher are handed out, so the bots keep
//     their reads and their flood limit for the person watching. The rest wait until playback has stopped.
//   - Among the jobs a worker may take, the highest priority goes first, and the oldest first when equal.
package taskpool

import (
	"context"
	"sort"
	"sync"
	"time"
)

// Who may run a job.
type Who int

const (
	// Either lets a bot or an account run the job.
	Either Who = iota
	// BotOnly is for work an account cannot do, such as reading file blocks for playback.
	BotOnly
	// UserOnly is for work a bot cannot do, such as looking through other channels or forwarding.
	UserOnly
)

// Priorities. The caller may use any number; these are the usual steps.
const (
	Background = 10  // crawling and housekeeping
	Normal     = 50  // new films and series, subtitles
	Requested  = 90  // something a person asked for in the panel
	Urgent     = 100 // a Seerr request, so someone is waiting for it
)

// Worker is one bot or one account asking for work.
type Worker struct {
	ID   int64
	User bool
}

// Job is one piece of work.
type Job struct {
	Name     string
	Who      Who
	Priority int
	// Run does the work with the worker that took it. A job that fails is not retried here.
	Run func(ctx context.Context, w Worker) error

	seq   int64
	added time.Time
}

// Pool holds the waiting jobs.
type Pool struct {
	mu      sync.Mutex
	wake    chan struct{} // closed and replaced whenever something changes
	jobs    []*Job
	seq     int64
	waiting map[bool]int // workers blocked in Next, by kind (true is an account)
	playing func() bool
}

// recheck is how often a waiting worker looks again, so jobs held back by playback start soon after it ends.
const recheck = time.Second

// SetPlaying tells the pool how to see that something is playing. Nil means nothing ever holds jobs back.
func (p *Pool) SetPlaying(f func() bool) {
	p.mu.Lock()
	p.playing = f
	p.mu.Unlock()
	p.mu.Lock()
	p.changed()
	p.mu.Unlock()
}

// New returns an empty pool.
func New() *Pool {
	return &Pool{wake: make(chan struct{}), waiting: map[bool]int{}}
}

// Add puts a job in the pool.
func (p *Pool) Add(j Job) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.seq++
	j.seq, j.added = p.seq, time.Now()
	p.jobs = append(p.jobs, &j)
	p.changed()
}

func (p *Pool) changed() {
	close(p.wake)
	p.wake = make(chan struct{})
}

// Next blocks until there is a job this worker should take, takes it out of the pool and returns it.
// It returns false when ctx ends.
func (p *Pool) Next(ctx context.Context, w Worker) (*Job, bool) {
	p.mu.Lock()
	p.waiting[w.User]++
	p.changed() // a bot waiting may free a shared job for an account only by leaving, so others re-check
	defer func() {
		p.waiting[w.User]--
		p.changed()
		p.mu.Unlock()
	}()
	for {
		if j := p.pick(w); j != nil {
			return j, true
		}
		wake := p.wake
		p.mu.Unlock()
		select {
		case <-wake:
			p.mu.Lock()
		case <-time.After(recheck):
			p.mu.Lock()
		case <-ctx.Done():
			p.mu.Lock()
			return nil, false
		}
	}
}

// pick chooses and removes the job w should run now. The caller holds the lock.
func (p *Pool) pick(w Worker) *Job {
	own := BotOnly // what only this kind of worker can do
	if w.User {
		own = UserOnly
	}
	hold := p.playing != nil && p.playing()
	var mine, either []*Job
	for _, j := range p.jobs {
		if hold && j.Priority < Requested {
			continue
		}
		switch j.Who {
		case own:
			mine = append(mine, j)
		case Either:
			either = append(either, j)
		}
	}
	list := mine
	if len(list) == 0 {
		// Nothing only this kind can do. An account helps with shared jobs only when no bot is free to take them.
		if w.User && p.waiting[false] > 0 {
			return nil
		}
		list = either
	}
	if len(list) == 0 {
		return nil
	}
	sort.SliceStable(list, func(a, b int) bool {
		if list[a].Priority != list[b].Priority {
			return list[a].Priority > list[b].Priority
		}
		return list[a].seq < list[b].seq
	})
	best := list[0]
	for i, j := range p.jobs {
		if j == best {
			p.jobs = append(p.jobs[:i], p.jobs[i+1:]...)
			break
		}
	}
	return best
}

// Counts is how many jobs wait, by who may run them.
type Counts struct{ Either, BotOnly, UserOnly int }

// Waiting reports what is in the pool, for the panel.
func (p *Pool) Waiting() Counts {
	p.mu.Lock()
	defer p.mu.Unlock()
	var c Counts
	for _, j := range p.jobs {
		switch j.Who {
		case BotOnly:
			c.BotOnly++
		case UserOnly:
			c.UserOnly++
		default:
			c.Either++
		}
	}
	return c
}

// Serve runs jobs for one worker until ctx ends. It is the loop each bot and each account runs.
func (p *Pool) Serve(ctx context.Context, w Worker, done func(j *Job, err error)) {
	for {
		j, ok := p.Next(ctx, w)
		if !ok {
			return
		}
		err := j.Run(ctx, w)
		if done != nil {
			done(j, err)
		}
	}
}
