// SPDX-License-Identifier: Apache-2.0
// Copyright 2018-2020 Authors of Cilium

// +build !privileged_tests

package cmd

import (
	"os"

	. "gopkg.in/check.v1"
)

func (s *DaemonSuite) TestMemoryMap(c *C) {
	pid := os.Getpid()
	m := memoryMap(pid)
	c.Assert(m, Not(Equals), "")
}
