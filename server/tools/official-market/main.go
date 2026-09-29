// Explicit official-market build; no placeholder origin or keys are shipped.
package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"

	"github.com/NovaWorks/zcard-next/server/internal/platform/marketprofile"
)

func main() {
	marketprofile.RequireOfficial = "true"
	p, e := marketprofile.Load()
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
	raw, e := json.Marshal(p)
	if e != nil {
		panic(e)
	}
	version := os.Getenv("VERSION")
	if version == "" {
		version = "dev"
	}
	if !regexp.MustCompile(`^[a-zA-Z0-9.+_-]+$`).MatchString(version) {
		fmt.Fprintln(os.Stderr, "invalid build version")
		os.Exit(1)
	}
	const prefix = "github.com/NovaWorks/zcard-next/server/internal/platform/marketprofile."
	flags := "-X main.Version=" + version + " -X " + prefix + "RequireOfficial=true -X " + prefix + "EmbeddedBase64=" + base64.StdEncoding.EncodeToString(raw)
	args := []string{"build", "-ldflags", flags, "-o", "./bin/zcard-official", "./cmd/zcard"}
	if os.Getenv("FULLSTACK") == "true" {
		args = append([]string{"build", "-tags", "fullstack"}, args[1:]...)
	}
	cmd := exec.Command("go", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()
	if e = cmd.Run(); e != nil {
		os.Exit(1)
	}
}
