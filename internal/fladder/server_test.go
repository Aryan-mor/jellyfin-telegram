package fladder

import (
	"strings"
	"testing"
)

func TestDockerPinsJellyfinAndSeerr(t *testing.T) {
	args := dockerArgs("http://192.168.2.75:8097", "http://192.168.2.75:8097/seerr")
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"BASE_URL=http://192.168.2.75:8097",
		"SEERR_BASE_URL=http://192.168.2.75:8097/seerr",
		"FLADDER_WEBPATH=/fladder",
		"127.0.0.1:15080:80",
		Image,
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %s in %s", want, joined)
		}
	}
	if strings.Contains(Image, "latest") || !strings.Contains(Image, "@sha256:") {
		t.Errorf("image is not pinned: %s", Image)
	}
}
