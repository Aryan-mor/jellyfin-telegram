package taskpool

import (
	"context"
	"testing"
	"time"
)

func take(t *testing.T, p *Pool, w Worker) *Job {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	j, ok := p.Next(ctx, w)
	if !ok {
		return nil
	}
	return j
}

func TestHighestPriorityThenOldest(t *testing.T) {
	p := New()
	p.Add(Job{Name: "crawl", Priority: Background})
	p.Add(Job{Name: "old", Priority: Normal})
	p.Add(Job{Name: "seerr", Priority: Urgent})
	p.Add(Job{Name: "new", Priority: Normal})
	for _, want := range []string{"seerr", "old", "new", "crawl"} {
		if j := take(t, p, Worker{ID: 1}); j == nil || j.Name != want {
			t.Fatalf("want %s, got %+v", want, j)
		}
	}
}

func TestAccountTakesItsOwnJobsBeforeSharedOnes(t *testing.T) {
	p := New()
	p.Add(Job{Name: "shared", Who: Either, Priority: Urgent})
	p.Add(Job{Name: "forward", Who: UserOnly, Priority: Background})
	if j := take(t, p, Worker{ID: 9, User: true}); j == nil || j.Name != "forward" {
		t.Fatalf("account took %+v", j)
	}
	// Only the shared job is left, and the account may help.
	if j := take(t, p, Worker{ID: 9, User: true}); j == nil || j.Name != "shared" {
		t.Fatalf("account took %+v", j)
	}
}

func TestBotNeverTakesAccountWork(t *testing.T) {
	p := New()
	p.Add(Job{Name: "forward", Who: UserOnly, Priority: Urgent})
	p.Add(Job{Name: "blocks", Who: BotOnly, Priority: Background})
	p.Add(Job{Name: "shared", Who: Either, Priority: Urgent})
	if j := take(t, p, Worker{ID: 1}); j == nil || j.Name != "blocks" {
		t.Fatalf("bot took %+v, want the job only bots can do first", j)
	}
	if j := take(t, p, Worker{ID: 1}); j == nil || j.Name != "shared" {
		t.Fatalf("bot took %+v", j)
	}
	if j := take(t, p, Worker{ID: 1}); j != nil {
		t.Fatalf("bot took account work: %+v", j)
	}
}

func TestAccountLeavesSharedJobToAWaitingBot(t *testing.T) {
	p := New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	got := make(chan string, 1)
	go func() {
		if j, ok := p.Next(ctx, Worker{ID: 1}); ok {
			got <- j.Name
		}
	}()
	time.Sleep(50 * time.Millisecond) // the bot is now waiting
	p.Add(Job{Name: "shared", Who: Either})
	select {
	case name := <-got:
		if name != "shared" {
			t.Fatalf("bot got %s", name)
		}
	case <-time.After(time.Second):
		t.Fatal("the waiting bot did not get the shared job")
	}
}

func TestAccountHelpsWhenNoBotIsFree(t *testing.T) {
	p := New()
	p.Add(Job{Name: "shared", Who: Either})
	if j := take(t, p, Worker{ID: 9, User: true}); j == nil || j.Name != "shared" {
		t.Fatalf("account took %+v", j)
	}
}

func TestPlaybackHoldsBackLowPriorityJobs(t *testing.T) {
	p := New()
	playing := true
	p.SetPlaying(func() bool { return playing })
	p.Add(Job{Name: "crawl", Priority: Background})
	p.Add(Job{Name: "seerr", Priority: Urgent})
	if j := take(t, p, Worker{ID: 1}); j == nil || j.Name != "seerr" {
		t.Fatalf("took %+v", j)
	}
	if j := take(t, p, Worker{ID: 1}); j != nil {
		t.Fatalf("took %s while playing", j.Name)
	}
	playing = false
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if j, ok := p.Next(ctx, Worker{ID: 1}); !ok || j.Name != "crawl" {
		t.Fatalf("after playback: %+v", j)
	}
}
