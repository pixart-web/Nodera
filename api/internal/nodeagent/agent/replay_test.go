package agent

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/nodera/nodera/internal/nodeagent/protocol"
	"github.com/nodera/nodera/internal/providers/mock"
)

// A validly signed command that is delivered twice is executed once.
func TestReplayedSignedCommandIsRefused(t *testing.T) {
	serverPub, serverPriv, _ := ed25519.GenerateKey(rand.Reader)
	_, agentPriv, _ := ed25519.GenerateKey(rand.Reader)
	st := State{AgentID: "agent-1", ServerPublicKey: base64.StdEncoding.EncodeToString(serverPub), PrivateSeedB64: base64.StdEncoding.EncodeToString(agentPriv.Seed())}
	set, h := mock.NewSet()
	a, err := New(st, set, nil)
	if err != nil {
		t.Fatal(err)
	}
	cmd, err := protocol.SignCommand(serverPriv, protocol.Command{
		ID: "cmd-1", AgentID: "agent-1", RequestID: "r", Op: "docker.create",
		Params: json.RawMessage(`{"name":"web1","image":"nginx:1.27"}`), ExpiresAt: time.Now().Add(time.Minute).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if res := a.handle(context.Background(), cmd); !res.OK {
		t.Fatalf("first delivery must run: %+v", res)
	}
	delete(h.Containers.M, "web1") // if the replay executed, the container would come back
	if res := a.handle(context.Background(), cmd); res.OK || h.Containers.M["web1"].Name != "" {
		t.Fatalf("a replayed command must not run again: %+v", res)
	}
	// A different command id with the same content is a new command.
	cmd2, _ := protocol.SignCommand(serverPriv, protocol.Command{
		ID: "cmd-2", AgentID: "agent-1", RequestID: "r", Op: "docker.create",
		Params: json.RawMessage(`{"name":"web1","image":"nginx:1.27"}`), ExpiresAt: time.Now().Add(time.Minute).Unix(),
	})
	if res := a.handle(context.Background(), cmd2); !res.OK {
		t.Fatalf("a distinct command must run: %+v", res)
	}
}

func TestExecutedSetForgetsExpiredCommands(t *testing.T) {
	a := &Agent{done: map[string]int64{}, Now: func() time.Time { return time.Unix(1000, 0) }}
	a.markExecuted(protocol.Command{ID: "old", ExpiresAt: 900})
	a.markExecuted(protocol.Command{ID: "new", ExpiresAt: 2000})
	if _, ok := a.done["old"]; ok && len(a.done) > 2 {
		t.Fatal("unexpected state")
	}
	a.markExecuted(protocol.Command{ID: "third", ExpiresAt: 2000})
	if _, ok := a.done["old"]; ok {
		t.Fatal("expired entries must be dropped")
	}
}
