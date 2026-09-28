package admincmd

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strconv"

	"github.com/NovaWorks/zcard-next/server/internal/conf"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/mods/audit"
	"github.com/NovaWorks/zcard-next/server/internal/mods/plugin"
	pc "github.com/NovaWorks/zcard-next/server/internal/platform/plugincontract"
	"github.com/NovaWorks/zcard-next/server/internal/platform/pluginstorage"
)

// RunPlugin uses the running process when the instance lock is held. Offline
// mutation takes the same lock as serve before opening the database.
func RunPlugin(args []string, version string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: zcard plugin <list|status|operation|import|enable|disable|disable-all|upgrade|rollback|uninstall> [flags]")
	}
	action := args[0]
	flags := flag.NewFlagSet("plugin "+action, flag.ContinueOnError)
	confDir := flags.String("conf", "./configs", "configuration directory")
	id := flags.String("id", "", "plugin ID")
	opID := flags.String("operation-id", "", "persistent operation UUID (printed if generated)")
	generation := flags.String("expected-generation", "", "expected generation; required for lifecycle mutations")
	sha := flags.String("digest", "", "signed artifact digest")
	descriptor := flags.String("descriptor", "", "descriptor JSON file")
	signature := flags.String("signature", "", "raw signature file")
	archive := flags.String("archive", "", "ZIP file")
	uploadAction := flags.String("action", "import", "upload action: import, upgrade or rollback")
	approve := flags.Bool("approve-scopes", false, "approve the two v1 read-only scopes for this exact digest")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	bc := &conf.Bootstrap{}
	if err := scanConf(*confDir, bc); err != nil {
		return err
	}
	if bc.Data == nil {
		return fmt.Errorf("database configuration required")
	}
	root, err := pluginstorage.ResolveRoot(*confDir, bc.Data.PluginDataDir)
	if err != nil {
		return err
	}
	bc.Data.PluginDataDir = root
	ctx := context.Background()
	lock, err := pluginstorage.Acquire(root)
	var execute func(context.Context, plugin.ControlRequest) (plugin.ControlReply, error)
	if errors.Is(err, pluginstorage.ErrLocked) {
		execute = func(ctx context.Context, in plugin.ControlRequest) (plugin.ControlReply, error) {
			return plugin.SendControl(ctx, root, in)
		}
	} else if err != nil {
		return err
	} else {
		defer lock.Close()
		d, closeDB, e := data.NewData(bc.Data)
		if e != nil {
			return e
		}
		defer closeDB()
		plugin.BuildVersion = version
		packages, e := plugin.ProvideFilePackages(bc.Data)
		if e != nil {
			return e
		}
		repo := plugin.NewRepo(d, plugin.NewCoordinator(), audit.NewAuditRepo(d, slog.Default()))
		m := plugin.NewManager(repo, packages, plugin.NewRuntimeLoader(packages))
		if e = m.InitializeStorage(ctx, root); e != nil {
			return e
		}
		execute = m.Control
	}
	if action == "disable-all" {
		if *opID == "" {
			*opID = newPluginOperationID()
		}
		fmt.Fprintf(os.Stderr, "operation_id=%s\n", *opID)
		return disableAll(ctx, root, *opID, execute)
	}
	in := plugin.ControlRequest{Action: action, PluginID: *id, OperationID: *opID}
	switch action {
	case "list", "status", "operation":
	case "import", "enable", "disable", "upgrade", "rollback", "uninstall":
		if *opID == "" {
			*opID = newPluginOperationID()
		}
		fmt.Fprintf(os.Stderr, "operation_id=%s\n", *opID)
		gen, e := strconv.ParseUint(*generation, 10, 64)
		if e != nil || strconv.FormatUint(gen, 10) != *generation {
			return fmt.Errorf("--expected-generation is required as a canonical decimal")
		}
		in.Command = plugin.Command{OperationID: *opID, PluginID: *id, Action: action, TargetDigest: *sha, ExpectedGeneration: gen}
		if *approve {
			in.Command.ApprovedScopes = []string{"order:validate", "plugin:config:read"}
		}
		if action == "import" {
			in.Command.Action = *uploadAction
			if in.Descriptor, e = boundedFile(*descriptor, pc.MaxJSONBytes); e != nil {
				return e
			}
			if in.Signature, e = boundedFile(*signature, 64); e != nil {
				return e
			}
			if in.Archive, e = boundedFile(*archive, pc.MaxArchiveBytes); e != nil {
				return e
			}
		} else {
			in.Action = "operate"
		}
	default:
		return fmt.Errorf("unknown plugin action %q", action)
	}
	out, err := execute(ctx, in)
	_ = json.NewEncoder(os.Stdout).Encode(out)
	return err
}
func boundedFile(path string, max int) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, int64(max+1)))
	if err == nil && len(b) > max {
		return nil, fmt.Errorf("file exceeds limit")
	}
	return b, err
}
func newPluginOperationID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	v := hex.EncodeToString(b[:])
	return v[:8] + "-" + v[8:12] + "-" + v[12:16] + "-" + v[16:20] + "-" + v[20:]
}
func disableAll(ctx context.Context, root, parent string, execute func(context.Context, plugin.ControlRequest) (plugin.ControlReply, error)) error {
	// The parent identity maps to a durable frozen list of per-plugin CAS commands.
	// It is never re-expanded on retry, which could otherwise disable new plugins.
	if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(parent) {
		return fmt.Errorf("invalid operation UUID")
	}
	dir := filepath.Join(root, "plugin-commands")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	path := filepath.Join(dir, parent+".json")
	var commands []plugin.Command
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		list, e := execute(ctx, plugin.ControlRequest{Action: "list"})
		if e != nil {
			return e
		}
		for _, s := range list.Statuses {
			if s.Uninstalled {
				continue
			}
			sum := sha256.Sum256([]byte(parent + "/" + s.State.PluginID))
			sum[6] = (sum[6] & 15) | 64
			sum[8] = (sum[8] & 63) | 128
			v := hex.EncodeToString(sum[:16])
			child := v[:8] + "-" + v[8:12] + "-" + v[12:16] + "-" + v[16:20] + "-" + v[20:]
			commands = append(commands, plugin.Command{OperationID: child, PluginID: s.State.PluginID, Action: "disable", ExpectedGeneration: s.State.DesiredGeneration})
		}
		raw, e = json.Marshal(commands)
		if e != nil {
			return e
		}
		f, e := os.CreateTemp(dir, ".plan-")
		if e != nil {
			return e
		}
		defer os.Remove(f.Name())
		if _, e = f.Write(raw); e == nil {
			e = f.Sync()
		}
		closeErr := f.Close()
		if e != nil {
			return e
		}
		if closeErr != nil {
			return closeErr
		}
		if e = os.Link(f.Name(), path); e != nil && !os.IsExist(e) {
			return e
		}
		parentDir, e := os.Open(dir)
		if e != nil {
			return e
		}
		e = parentDir.Sync()
		_ = parentDir.Close()
		if e != nil {
			return e
		}
		raw, e = os.ReadFile(path)
		if e != nil {
			return e
		}
	} else if err != nil {
		return err
	}
	if err = json.Unmarshal(raw, &commands); err != nil {
		return err
	}
	var failed error
	for _, c := range commands {
		out, e := execute(ctx, plugin.ControlRequest{Action: "operate", Command: c})
		_ = json.NewEncoder(os.Stdout).Encode(out)
		if e != nil {
			failed = e
		}
	}
	return failed
}
