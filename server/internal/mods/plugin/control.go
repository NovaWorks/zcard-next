package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/mods/plugin/port"
	pc "github.com/NovaWorks/zcard-next/server/internal/platform/plugincontract"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
)

// ControlRequest is sent only over the 0600 local socket. The server replaces
// the actor with an OS-authorized system principal; no admin token is borrowed.
type ControlRequest struct {
	Action      string  `json:"action"`
	Command     Command `json:"command"`
	PluginID    string  `json:"plugin_id"`
	OperationID string  `json:"operation_id"`
	Descriptor  []byte  `json:"descriptor,omitempty"`
	Signature   []byte  `json:"signature,omitempty"`
	Archive     []byte  `json:"archive,omitempty"`
}
type ControlReply struct {
	Operation *Operation `json:"operation,omitempty"`
	Status    *Status    `json:"status,omitempty"`
	Statuses  []Status   `json:"statuses,omitempty"`
	Error     string     `json:"error,omitempty"`
	Affected  int        `json:"affected"`
}

func LocalActor() port.Actor {
	return port.Actor{ScopeVerified: true, InstanceAdmin: true, LocalOperator: true}
}
func (m *Manager) Control(ctx context.Context, in ControlRequest) (ControlReply, error) {
	ctx = tenancy.WithContext(ctx, tenancy.Main())
	var out ControlReply
	in.Command.Actor = LocalActor()
	switch in.Action {
	case "status":
		s, err := m.Status(ctx, in.PluginID)
		out.Status = &s
		return out, err
	case "list":
		ids, err := m.repo.installedIDs(ctx)
		if err != nil {
			return out, err
		}
		for _, id := range ids {
			s, e := m.Status(ctx, id)
			if e != nil {
				return out, e
			}
			out.Statuses = append(out.Statuses, s)
		}
		return out, nil
	case "operation":
		op, err := m.repo.operation(ctx, in.OperationID)
		out.Operation = &op
		return out, err
	case "import":
		op, err := m.Import(ctx, in.Command, in.Descriptor, in.Signature, bytes.NewReader(in.Archive))
		out.Operation = &op
		return out, err
	case "operate":
		n, _, err := m.repo.impact(ctx, in.Command.PluginID, 0)
		if err != nil {
			return out, err
		}
		out.Affected = n
		op, err := m.Operate(ctx, in.Command)
		out.Operation = &op
		s, _ := m.Status(ctx, in.Command.PluginID)
		out.Status = &s
		return out, err
	default:
		return out, contractError(pc.InvalidContract, "unknown control action")
	}
}
func (m *Manager) StartControl(root string) (func(), error) {
	dir := filepath.Join(root, ".plugin-control")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "socket")
	if st, err := os.Lstat(path); err == nil {
		if st.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("control path is not a socket")
		}
		if err = os.Remove(path); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err = os.Chmod(path, 0600); err != nil {
		_ = listener.Close()
		return nil, err
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/command" {
			http.NotFound(w, r)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 12<<20)
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		var in ControlRequest
		if err := decoder.Decode(&in); err != nil {
			http.Error(w, "invalid control request", 400)
			return
		}
		var extra any
		if decoder.Decode(&extra) != io.EOF {
			http.Error(w, "trailing request data", 400)
			return
		}
		out, err := m.Control(r.Context(), in)
		if err != nil {
			out.Error = err.Error()
		}
		w.Header().Set("Content-Type", "application/json")
		if err != nil {
			w.WriteHeader(http.StatusConflict)
		}
		_ = json.NewEncoder(w).Encode(out)
	})
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second}
	go func() { _ = srv.Serve(listener) }()
	return func() { _ = srv.Close(); _ = listener.Close() }, nil
}
func SendControl(ctx context.Context, root string, in ControlRequest) (ControlReply, error) {
	var out ControlReply
	raw, err := json.Marshal(in)
	if err != nil {
		return out, err
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", filepath.Join(root, ".plugin-control", "socket"))
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 30 * time.Second}
	req, err := http.NewRequestWithContext(ctx, "POST", "http://localhost/command", bytes.NewReader(raw))
	if err != nil {
		return out, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return out, fmt.Errorf("control result unknown; query the same operation ID: %w", err)
	}
	defer resp.Body.Close()
	if err = json.NewDecoder(io.LimitReader(resp.Body, pc.MaxJSONBytes*16)).Decode(&out); err != nil {
		return out, err
	}
	if out.Error != "" {
		return out, fmt.Errorf("%s", out.Error)
	}
	return out, nil
}
