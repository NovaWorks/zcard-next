package updater

import (
	"errors"
	"os"
	"strings"
)

var ErrContainerUpdate = errors.New("当前为 Docker/容器部署，请通过重建镜像和容器升级或回退，不支持在容器内替换程序")

// IsContainer checks an image marker and common Docker/Podman/Kubernetes markers.
// Keep this independent of the user-selected process supervisor.
func IsContainer() bool { return detectContainer(os.Getenv, os.ReadFile) }

func detectContainer(getenv func(string) string, readFile func(string) ([]byte, error)) bool {
	if getenv("ZCARD_CONTAINER") == "1" {
		return true
	}
	for _, path := range []string{"/.dockerenv", "/run/.containerenv"} {
		if _, err := readFile(path); err == nil {
			return true
		}
	}
	if data, err := readFile("/proc/1/cgroup"); err == nil {
		for _, marker := range []string{"/docker/", "docker-", "kubepods", "libpod-"} {
			if strings.Contains(string(data), marker) {
				return true
			}
		}
	}
	return false
}

// HasUpdate leaves an unknown container build unclassified instead of declaring
// every release newer than dev. The UI explains how to rebuild such images.
func HasUpdate(latest, current string) bool {
	return (!IsContainer() || IsSemver(current)) && CompareSemver(latest, current) > 0
}
