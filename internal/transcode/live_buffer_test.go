package transcode

import (
	"testing"
	"time"
)

func TestLiveJitterDelay(t *testing.T) {

	j := NewLiveJitter(10 * time.Second)

	if delay := j.Delay(0); delay != 0 {
		t.Fatalf("startup delay = %v, want 0", delay)
	}

	j.Observe(12 * time.Second)

	if delay := j.Delay(0); delay != 2*time.Second {
		t.Fatalf("over-full delay = %v, want 2s", delay)
	}

	if delay := j.Delay(2 * time.Second); delay != 0 {
		t.Fatalf("at-target delay = %v, want 0", delay)
	}

	j.Observe(5 * time.Second)

	if delay := j.Delay(0); delay != 2*time.Second {
		t.Fatalf("head should not move backward; delay = %v, want 2s", delay)
	}

}
