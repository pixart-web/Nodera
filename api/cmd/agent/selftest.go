package main

// `nodera-agent selftest` is a deployment-validation aid: it sends deliberately
// bad requests, signed with this agent's own identity, at the live control plane
// and checks that every one is refused. It proves replay protection, signature
// binding and clock-skew rejection on the REAL path (reverse proxy included).
// It sends nothing destructive: the only accepted request is one ordinary
// heartbeat, and the agent keeps running.

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/nodera/nodera/internal/nodeagent/agent"
	"github.com/nodera/nodera/internal/nodeagent/protocol"
)

func selftest(args []string) {
	fs := flag.NewFlagSet("selftest", flag.ExitOnError)
	state := fs.String("state", defaultState(), "state file path")
	_ = fs.Parse(args)
	st, err := agent.LoadState(*state)
	if err != nil {
		log.Fatalf("load state: %v", err)
	}
	if err := agent.ValidateAPIURL(st.APIURL); err != nil {
		log.Fatalf("api url in state: %v", err)
	}
	seed, err := base64.StdEncoding.DecodeString(st.PrivateSeedB64)
	if err != nil || len(seed) != ed25519.SeedSize {
		log.Fatal("invalid private key in state")
	}
	priv := ed25519.NewKeyFromSeed(seed)
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	hb, _ := json.Marshal(protocol.Heartbeat{Version: agent.Version, Status: "online"})
	other2 := []byte(`{"version":"tampered","status":"online"}`)

	nonce := func() string {
		b := make([]byte, 18)
		_, _ = rand.Read(b)
		return base64.RawURLEncoding.EncodeToString(b)
	}
	const path = "/agent/v1/heartbeat"
	send := func(key ed25519.PrivateKey, id string, signedBody, sentBody []byte, at time.Time, n string, unsigned bool) int {
		req, _ := http.NewRequest(http.MethodPost, st.APIURL+path, bytes.NewReader(sentBody))
		req.Header.Set("Content-Type", "application/json")
		if !unsigned {
			for k, v := range protocol.SignRequest(key, id, http.MethodPost, path, signedBody, at, n) {
				req.Header.Set(k, v)
			}
		}
		resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
		if err != nil {
			log.Fatalf("request failed: %v", err)
		}
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, resp.Body)
		return resp.StatusCode
	}

	failed := 0
	check := func(name string, code int, wantOK bool) {
		ok := (code >= 200 && code < 300) == wantOK
		verdict := "PASS"
		if !ok {
			verdict, failed = "FAIL", failed+1
		}
		fmt.Printf("%s  %s (HTTP %d)\n", verdict, name, code)
	}

	n := nonce()
	check("valid signed heartbeat is accepted", send(priv, st.AgentID, hb, hb, time.Now(), n, false), true)
	check("replayed nonce is rejected", send(priv, st.AgentID, hb, hb, time.Now(), n, false), false)
	check("tampered body is rejected", send(priv, st.AgentID, hb, other2, time.Now(), nonce(), false), false)
	check("stale timestamp (-10 min) is rejected", send(priv, st.AgentID, hb, hb, time.Now().Add(-10*time.Minute), nonce(), false), false)
	check("future timestamp (+10 min) is rejected", send(priv, st.AgentID, hb, hb, time.Now().Add(10*time.Minute), nonce(), false), false)
	check("request signed with a different key is rejected", send(other, st.AgentID, hb, hb, time.Now(), nonce(), false), false)
	check("unknown agent id is rejected", send(priv, "00000000-0000-0000-0000-000000000000", hb, hb, time.Now(), nonce(), false), false)
	check("unsigned request is rejected", send(priv, st.AgentID, hb, hb, time.Now(), nonce(), true), false)
	if failed > 0 {
		fmt.Printf("\n%d check(s) FAILED\n", failed)
		os.Exit(1)
	}
	fmt.Println("\nall agent negative checks passed")
}
