// Copyright 2021 Authors of Cilium
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cmd

import (
	"fmt"
	"os/exec"
	"reflect"
	"regexp"
	"strings"

	"github.com/cilium/cilium/api/v1/client/daemon"
	"github.com/cilium/cilium/api/v1/models"
	"github.com/cilium/cilium/pkg/command"
	"github.com/cilium/cilium/pkg/common"
	"github.com/prometheus/procfs"
	"github.com/vishvananda/netlink"

	"github.com/spf13/cobra"
)

const (
	// Cilium uses reqid 1 to tie the IPsec security policies to their matching state
	ciliumReqId = "1"
)

type void struct{}

var (
	voidType    void
	countErrors int
	regex       = regexp.MustCompile("oseq[[:blank:]](0[xX][[:xdigit:]]+)?")
)

var encryptStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Display the current encryption state",
	Run: func(cmd *cobra.Command, args []string) {
		common.RequireRootPrivilege("cilium encrypt status")
		getEncryptionMode()
	},
}

func init() {
	encryptCmd.AddCommand(encryptStatusCmd)
	command.AddJSONOutput(encryptStatusCmd)
}

func getXfrmStats(mountPoint string) (int, map[string]int) {
	fs, err := procfs.NewDefaultFS()
	if mountPoint != "" {
		fs, err = procfs.NewFS(mountPoint)
	}
	if err != nil {
		Fatalf("Cannot get a new proc FS: %s", err)
	}
	stats, err := fs.NewXfrmStat()
	if err != nil {
		Fatalf("Failed to read xfrm statistics: %s", err)
	}
	v := reflect.ValueOf(stats)
	errorMap := make(map[string]int)
	if v.Type().Kind() == reflect.Struct {
		for i := 0; i < v.NumField(); i++ {
			name := v.Type().Field(i).Name
			value := v.Field(i).Interface().(int)
			if value != 0 {
				countErrors += value
				errorMap[name] = value
			}
		}
	}
	return countErrors, errorMap
}

func countUniqueIPsecKeys() int {
	// trying to mimic set type data structure
	// using void data type as struct{} because it does not use any memory
	keys := make(map[string]void)
	xfrmStates, err := netlink.XfrmStateList(netlink.FAMILY_ALL)
	if err != nil {
		Fatalf("Cannot get xfrm state: %s", err)
	}
	for _, v := range xfrmStates {
		keys[string(v.Aead.Key)] = voidType
	}

	return len(keys)
}

func maxSequenceNumber() string {
	maxSeqNum := "0"
	out, err := exec.Command("ip", "xfrm", "state", "list", "reqid", ciliumReqId).Output()
	if err != nil {
		Fatalf("Cannot get xfrm states: %s", err)
	}
	commandOutput := string(out)
	lines := strings.Split(commandOutput, "\n")
	for _, line := range lines {
		matched := regex.FindStringSubmatchIndex(line)
		if matched != nil {
			oseq := line[matched[2]:matched[3]]
			if oseq > maxSeqNum {
				maxSeqNum = oseq
			}
		}
	}
	if maxSeqNum == "0" {
		return "N/A"
	}
	return fmt.Sprintf("%s/0xffffffff", maxSeqNum)
}

func getEncryptionMode() {
	params := daemon.NewGetHealthzParamsWithTimeout(timeout)
	params.SetBrief(&brief)
	resp, err := client.Daemon.GetHealthz(params)
	if err != nil {
		Fatalf("Cannot get daemon encryption status: %s", err)
	}
	encryptionStatusResponse := resp.Payload.Encryption
	fmt.Printf("Encryption: %-26s\n", encryptionStatusResponse.Mode)

	switch encryptionStatusResponse.Mode {
	case models.EncryptionStatusModeIPsec:
		dumpIPsecStatus()
	}

}

func dumpIPsecStatus() {
	keys := countUniqueIPsecKeys()
	oseq := maxSequenceNumber()
	fmt.Printf("Keys in use: %-26d\n", keys)
	fmt.Printf("Max Seq. Number: %s\n", oseq)
	errCount, errMap := getXfrmStats("")
	fmt.Printf("Errors: %-26d\n", errCount)
	if errCount != 0 {
		for k, v := range errMap {
			fmt.Printf("\t%s: %-26d\n", k, v)
		}
	}
}
