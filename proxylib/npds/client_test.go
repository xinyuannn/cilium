// SPDX-License-Identifier: Apache-2.0
// Copyright 2018 Authors of Cilium

// +build !privileged_tests

package npds

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/cilium/cilium/pkg/completion"
	"github.com/cilium/cilium/pkg/envoy"
	"github.com/cilium/cilium/proxylib/test"

	"github.com/cilium/proxy/go/cilium/api"
	envoy_service_disacovery "github.com/cilium/proxy/go/envoy/service/discovery/v3"
	log "github.com/sirupsen/logrus"
	. "gopkg.in/check.v1"
)

// Hook up gocheck into the "go test" runner.
func Test(t *testing.T) {
	TestingT(t)
}

type ClientSuite struct {
	acks  int
	nacks int
}

var _ = Suite(&ClientSuite{})

const (
	TestTimeout      = 10 * time.Second
	CacheUpdateDelay = 250 * time.Millisecond
)

var resources = []*cilium.NetworkPolicy{
	{Name: "resource0"},
	{Name: "resource1"},
	{Name: "resource2"},
}

// UpsertNetworkPolicy must only be used for testing!
func (cs *ClientSuite) UpsertNetworkPolicy(c *C, s *envoy.XDSServer, p *cilium.NetworkPolicy) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	wg := completion.NewWaitGroup(ctx)

	callback := func(err error) {
		if err == nil {
			log.Debug("ACK Callback called")
			cs.acks++
		} else {
			log.Debug("NACK Callback called")
			cs.nacks++
		}
	}

	s.NetworkPolicyMutator.Upsert(envoy.NetworkPolicyTypeURL, p.Name, p, []string{"127.0.0.1"}, wg, callback)
}

type updater struct{}

func (u *updater) PolicyUpdate(resp *envoy_service_disacovery.DiscoveryResponse) error {
	log.Debugf("Received policy update: %s", resp.String())
	return nil
}

func (s *ClientSuite) TestRequestAllResources(c *C) {
	var updater *updater
	xdsPath := filepath.Join(test.Tmpdir, "xds.sock")
	client1 := NewClient(xdsPath, "sidecar~127.0.0.1~v0.default~default.svc.cluster.local", updater)
	if client1 == nil {
		c.Error("NewClient() failed")
	}

	// Start another client, which will never connect
	xdsPath2 := filepath.Join(test.Tmpdir, "xds.sock2")
	client2 := NewClient(xdsPath2, "sidecar~127.0.0.2~v0.default~default.svc.cluster.local", updater)
	if client2 == nil {
		c.Error("NewClient() failed")
	}

	// Some wait before server is made available
	time.Sleep(500 * time.Millisecond)
	xdsServer := envoy.StartXDSServer(test.Tmpdir)
	time.Sleep(500 * time.Millisecond)

	// Create version 1 with resource 0.
	s.UpsertNetworkPolicy(c, xdsServer, resources[0])

	time.Sleep(DialDelay * BackOffLimit)
	c.Assert(s.acks, Equals, 1)
	c.Assert(s.nacks, Equals, 0)
}
