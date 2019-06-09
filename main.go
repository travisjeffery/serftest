package main

import (
	"bytes"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/davecgh/go-spew/spew"
	"github.com/hashicorp/serf/serf"
)

func main() {
	srv := &server{}
	if err := srv.setupSerf(); err != nil {
		panic(err)
	}
	go srv.logState()
	srv.eventHandler()
}

type server struct {
	serf   *serf.Serf
	events chan serf.Event
}

func (s *server) setupSerf() error {
	cfg := serf.DefaultConfig()
	cfg.Init()
	cfg.NodeName = os.Getenv("NODE_NAME")
	portStr := os.Getenv("NODE_PORT")
	port, err := strconv.Atoi(portStr)
	if err != nil {
		panic(err)
	}
	cfg.MemberlistConfig.BindPort = port
	cfg.MemberlistConfig.BindAddr = os.Getenv("NODE_ADDR")
	s.events = make(chan serf.Event)
	cfg.EventCh = s.events
	s.serf, err = serf.Create(cfg)
	if err != nil {
		return err
	}
	if os.Getenv("CLUSTER_ADDR") != "" {
		if _, err = s.serf.Join([]string{os.Getenv("CLUSTER_ADDR")}, true); err != nil {
			return err
		}
	}
	return nil
}

func (s *server) eventHandler() {
	for event := range s.events {
		fmt.Printf("serf event: %v", event)
	}
}

func (s *server) logState() {
	for {
		time.Sleep(1 * time.Second)
		var buf bytes.Buffer
		buf.WriteString("members:\n")
		for _, member := range s.serf.Members() {
			buf.WriteString(fmt.Sprintf("member: %s\n", member.Name))
			buf.WriteString(spew.Sdump(member))
			buf.WriteString("\n")
		}
		fmt.Println(buf.String())
	}
}
