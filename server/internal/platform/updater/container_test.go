package updater

import (
	"os"
	"testing"
)

func TestContainerDetection(t *testing.T) {
	for _, tc := range []struct {
		name, env, path, content string
		want                     bool
	}{
		{name: "bare process"},
		{name: "image marker", env: "1", want: true},
		{name: "docker", path: "/.dockerenv", want: true},
		{name: "podman", path: "/run/.containerenv", want: true},
		{name: "docker cgroup", path: "/proc/1/cgroup", content: "0::/system.slice/docker-123.scope", want: true},
		{name: "kubernetes", path: "/proc/1/cgroup", content: "0::/kubepods/pod123", want: true},
		{name: "systemd host", path: "/proc/1/cgroup", content: "0::/init.scope"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := detectContainer(func(string) string { return tc.env }, func(path string) ([]byte, error) {
				if path == tc.path {
					return []byte(tc.content), nil
				}
				return nil, os.ErrNotExist
			})
			if got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestContainerUpdateVersionAndSupervisor(t *testing.T) {
	t.Setenv("ZCARD_CONTAINER", "1")
	t.Setenv("ZCARD_SUPERVISOR", "systemd")
	if DetectSupervisor() != "docker" {
		t.Fatal("container must take priority over supervisor override")
	}
	for _, tc := range []struct {
		current string
		want    bool
	}{
		{"dev", false}, {"", false}, {"v1.2.58", false}, {"1.2.58", false}, {"v1.2.57", true}, {"v1.2.59", false},
	} {
		if HasUpdate("v1.2.58", tc.current) != tc.want {
			t.Fatalf("current=%q", tc.current)
		}
	}
}
