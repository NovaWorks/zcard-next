package plugin

import (
	"archive/zip"
	"bytes"
	"context"
	"io"

	"github.com/NovaWorks/zcard-next/server/internal/mods/plugin/port"
	pc "github.com/NovaWorks/zcard-next/server/internal/platform/plugincontract"
	"github.com/NovaWorks/zcard-next/server/internal/platform/pluginruntime"
)

type RuntimeLoader struct {
	packages port.PackageStore
	engine   *pluginruntime.Engine
}

func NewRuntimeLoader(p port.PackageStore) *RuntimeLoader {
	return &RuntimeLoader{packages: p, engine: pluginruntime.NewEngine()}
}
func (l *RuntimeLoader) Prepare(ctx context.Context, a port.Artifact) (port.PreparedRuntime, error) {
	verified, reader, err := l.packages.Open(ctx, a.Descriptor.ArchiveSHA256)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	if verified.Manifest.ID != a.Manifest.ID {
		return nil, contractError(pc.InvalidContract, "artifact identity mismatch")
	}
	b, err := io.ReadAll(io.LimitReader(reader, pc.MaxArchiveBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > pc.MaxArchiveBytes {
		return nil, contractError(pc.InvalidContract, "archive limit")
	}
	z, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		return nil, err
	}
	for _, f := range z.File {
		if f.Name == "main.js" {
			r, e := f.Open()
			if e != nil {
				return nil, e
			}
			raw, e := io.ReadAll(io.LimitReader(r, pc.MaxScriptBytes+1))
			r.Close()
			if e != nil {
				return nil, e
			}
			rt, err := l.engine.Prepare(ctx, a.Descriptor.ArchiveSHA256, string(raw))
			if err != nil {
				return nil, err
			}
			return &artifactRuntime{Runtime: rt, digest: a.Descriptor.ArchiveSHA256, manifest: verified.Manifest}, nil
		}
	}
	return nil, contractError(pc.InvalidContract, "entry missing")
}

type artifactRuntime struct {
	*pluginruntime.Runtime
	digest   string
	manifest pc.Manifest
}
