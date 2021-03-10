/*
   Copyright (C) nerdctl authors.
   Copyright (C) containerd authors.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

package testutil

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/AkihiroSuda/nerdctl/pkg/buildkitutil"
	"github.com/AkihiroSuda/nerdctl/pkg/defaults"
	"github.com/pkg/errors"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/icmd"
)

type Base struct {
	T                testing.TB
	Target           Target
	DaemonIsKillable bool
	Binary           string
	Args             []string
}

func (b *Base) Cmd(args ...string) *Cmd {
	icmdCmd := icmd.Command(b.Binary, append(b.Args, args...)...)
	cmd := &Cmd{
		Cmd:  icmdCmd,
		Base: b,
	}
	return cmd
}

func (b *Base) systemctlTarget() string {
	switch b.Target {
	case Nerdctl:
		return "containerd.service"
	case Docker:
		return "docker.service"
	default:
		b.T.Fatalf("unexpected target %q", b.Target)
		return ""
	}
}

func (b *Base) systemctlArgs() []string {
	var systemctlArgs []string
	if os.Geteuid() != 0 {
		systemctlArgs = append(systemctlArgs, "--user")
	}
	return systemctlArgs
}

func (b *Base) KillDaemon() {
	b.T.Helper()
	if !b.DaemonIsKillable {
		b.T.Skip("daemon is not killable (hint: set \"-test.kill-daemon\")")
	}
	target := b.systemctlTarget()
	b.T.Logf("killing %q", target)
	cmdKill := exec.Command("systemctl",
		append(b.systemctlArgs(),
			[]string{"kill", "-s", "KILL", target}...)...)
	if out, err := cmdKill.CombinedOutput(); err != nil {
		err = errors.Wrapf(err, "cannot kill %q: %q", target, string(out))
		b.T.Fatal(err)
	}
	// the daemon should restart automatically
}

func (b *Base) EnsureDaemonActive() {
	b.T.Helper()
	target := b.systemctlTarget()
	b.T.Logf("checking activity of %q", target)
	systemctlArgs := b.systemctlArgs()
	const (
		maxRetry = 30
		sleep    = 3 * time.Second
	)
	for i := 0; i < maxRetry; i++ {
		cmd := exec.Command("systemctl",
			append(systemctlArgs,
				[]string{"is-active", target}...)...)
		out, err := cmd.CombinedOutput()
		b.T.Logf("(retry=%d) %s", i, string(out))
		if err == nil {
			b.T.Logf("daemon %q is now running", target)
			return
		}
		time.Sleep(sleep)
	}
	b.T.Fatalf("daemon %q not running", target)
}

type Cmd struct {
	icmd.Cmd
	*Base
}

func (c *Cmd) Run() *icmd.Result {
	c.Base.T.Helper()
	return icmd.RunCmd(c.Cmd)
}

func (c *Cmd) Assert(expected icmd.Expected) {
	c.Base.T.Helper()
	c.Run().Assert(c.Base.T, expected)
}

func (c *Cmd) AssertOK() {
	c.Base.T.Helper()
	expected := icmd.Expected{}
	c.Assert(expected)
}

func (c *Cmd) AssertFail() {
	c.Base.T.Helper()
	res := c.Run()
	assert.Assert(c.Base.T, res.ExitCode != 0)
}

func (c *Cmd) AssertOut(s string) {
	c.Base.T.Helper()
	expected := icmd.Expected{
		Out: s,
	}
	c.Assert(expected)
}

func (c *Cmd) AssertOutWithFunc(fn func(stdout string) error) {
	c.Base.T.Helper()
	res := c.Run()
	assert.Equal(c.Base.T, 0, res.ExitCode, res.Combined())
	assert.NilError(c.Base.T, fn(res.Stdout()), res.Combined())
}

type Target = string

const (
	Nerdctl = Target("nerdctl")
	Docker  = Target("docker")
)

var (
	flagTestTarget     Target
	flagTestKillDaemon bool
)

func M(m *testing.M) {
	flag.StringVar(&flagTestTarget, "test.target", Nerdctl, "target to test")
	flag.BoolVar(&flagTestKillDaemon, "test.kill-daemon", false, "enable tests that kill the daemon")
	flag.Parse()
	fmt.Printf("test target: %q\n", flagTestTarget)
	os.Exit(m.Run())
}

func GetTarget() string {
	if flagTestTarget == "" {
		panic("GetTarget() was called without calling M()")
	}
	return flagTestTarget
}

func GetDaemonIsKillable() bool {
	return flagTestKillDaemon
}

func DockerIncompatible(t testing.TB) {
	if GetTarget() == Docker {
		t.Skip("test is incompatible with Docker")
	}
}

func RequiresBuild(t testing.TB) {
	if GetTarget() == Nerdctl {
		buildkitHost := defaults.BuildKitHost()
		t.Logf("buildkitHost=%q", buildkitHost)
		if err := buildkitutil.PingBKDaemon(buildkitHost); err != nil {
			t.Skipf("test requires buildkitd: %+v", err)
		}
	}
}

const Namespace = "nerdctl-test"

func NewBase(t *testing.T) *Base {
	base := &Base{
		T:                t,
		Target:           GetTarget(),
		DaemonIsKillable: GetDaemonIsKillable(),
	}
	var err error
	switch base.Target {
	case Nerdctl:
		base.Binary, err = exec.LookPath("nerdctl")
		if err != nil {
			t.Fatal(err)
		}
		base.Args = []string{"--namespace=" + Namespace}
	case Docker:
		base.Binary, err = exec.LookPath("docker")
		if err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatalf("unknown test target %q", base.Target)
	}
	return base
}

// use GCR mirror to avoid hitting Docker Hub rate limit
const (
	AlpineImage                 = "mirror.gcr.io/library/alpine:3.13"
	NginxAlpineImage            = "mirror.gcr.io/library/nginx:1.19-alpine"
	NginxAlpineIndexHTMLSnippet = "<title>Welcome to nginx!</title>"
	RegistryImage               = "mirror.gcr.io/library/registry:2"
)