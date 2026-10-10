// Command agent is the Nodera Node Agent: a small pull-model daemon that runs
// on a managed node. It never listens on a port; it enrols once with a
// one-time token, then polls the control plane for signed, allowlisted
// commands.
//
//	nodera-agent enroll --api https://nodera.example --token <one-time-token>
//	nodera-agent run
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nodera/nodera/internal/nodeagent/agent"
	"github.com/nodera/nodera/internal/platform/netpolicy"
	"github.com/nodera/nodera/internal/providers"
	"github.com/nodera/nodera/internal/providers/docker"
	"github.com/nodera/nodera/internal/providers/local"
	"github.com/nodera/nodera/internal/providers/mock"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "enroll":
		enroll(os.Args[2:])
	case "run":
		run(os.Args[2:])
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: nodera-agent enroll --api URL --token TOKEN [--state PATH] | run [--state PATH] [--driver docker|mock] [--data DIR]")
	os.Exit(2)
}

func defaultState() string {
	if v := os.Getenv("NODERA_AGENT_STATE"); v != "" {
		return v
	}
	return "nodera-agent.json"
}

func enroll(args []string) {
	fs := flag.NewFlagSet("enroll", flag.ExitOnError)
	api := fs.String("api", os.Getenv("NODERA_API_URL"), "control plane base URL")
	token := fs.String("token", os.Getenv("NODERA_ENROLL_TOKEN"), "one-time enrolment token")
	state := fs.String("state", defaultState(), "state file path")
	_ = fs.Parse(args)
	if *api == "" || *token == "" {
		usage()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := agent.Enroll(ctx, *api, *token, nil)
	if err != nil {
		log.Fatalf("enrol: %v", err)
	}
	if err := agent.SaveState(*state, st); err != nil {
		log.Fatalf("save state: %v", err)
	}
	log.Printf("enrolled as agent %s; state written to %s", st.AgentID, *state)
}

func run(args []string) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	state := fs.String("state", defaultState(), "state file path")
	driver := fs.String("driver", getenv("NODERA_AGENT_DRIVER", "docker"), "docker | mock")
	data := fs.String("data", getenv("NODERA_AGENT_DATA", "./agent-data"), "data directory for filesystem operations")
	interval := fs.Duration("interval", 5*time.Second, "poll interval")
	_ = fs.Parse(args)

	st, err := agent.LoadState(*state)
	if err != nil {
		log.Fatalf("load state (run enroll first): %v", err)
	}
	set, err := local.NewSet(*data, netpolicy.Policy{})
	if err != nil {
		log.Fatalf("providers: %v", err)
	}
	switch *driver {
	case "docker":
		set.Containers = docker.New("docker")
	case "mock":
		m, _ := mock.NewSet()
		set.Containers = m.Containers
		log.Printf("WARNING: mock driver — container operations are simulated")
	default:
		log.Fatalf("unknown driver %q", *driver)
	}
	var _ providers.Set = set
	a, err := agent.New(st, set, nil)
	if err != nil {
		log.Fatalf("agent: %v", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	a.Run(ctx, *interval, log.Printf)
}

func getenv(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
