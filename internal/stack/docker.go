package stack

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/Aryan-mor/jellyfin-telegram/internal/proc"
)

// Available reports whether the docker command is on PATH.
func Available() error {
	_, err := dockerBin()
	return err
}

func dockerBin() (string, error) {
	path, err := exec.LookPath("docker")
	if err != nil {
		return "", errors.New("downloads need Docker")
	}
	return path, nil
}

func docker(ctx context.Context, args ...string) (string, error) {
	bin, err := dockerBin()
	if err != nil {
		return "", err
	}
	cmd := proc.Command(ctx, bin, args...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err = cmd.Run()
	out := strings.TrimSpace(buf.String())
	if err != nil {
		if len(out) > 300 {
			out = out[len(out)-300:]
		}
		return out, fmt.Errorf("docker %s: %w (%s)", args[0], err, out)
	}
	return out, nil
}

func dockerOK(ctx context.Context, args ...string) bool {
	bin, err := dockerBin()
	if err != nil {
		return false
	}
	return proc.Command(ctx, bin, args...).Run() == nil
}

func ensureNetwork(ctx context.Context) error {
	if dockerOK(ctx, "network", "inspect", Network) {
		return nil
	}
	_, err := docker(ctx, "network", "create", Network)
	return err
}

func pull(ctx context.Context, image string) error {
	if dockerOK(ctx, "image", "inspect", image) {
		return nil
	}
	_, err := docker(ctx, "pull", image)
	return err
}

func containerRunning(ctx context.Context, name string) bool {
	out, err := docker(ctx, "inspect", "-f", "{{.State.Running}}", name)
	return err == nil && out == "true"
}

func containerExists(ctx context.Context, name string) bool {
	return dockerOK(ctx, "inspect", name)
}

func ensureContainer(ctx context.Context, name string, runArgs []string) error {
	if containerRunning(ctx, name) {
		return nil
	}
	if containerExists(ctx, name) {
		_, err := docker(ctx, "start", name)
		return err
	}
	args := append([]string{"run", "-d", "--name", name, "--network", Network, "--pull", "never"}, runArgs...)
	_, err := docker(ctx, args...)
	return err
}

func removeContainers(ctx context.Context) {
	for _, name := range []string{QbitName, RadarrName, SonarrName, BitmagnetName, PostgresName} {
		_, _ = docker(ctx, "rm", "-f", name)
	}
}
