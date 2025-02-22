package tests

import (
	"bytes"
	"context"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ChronosX88/go-gun/gun"
	"github.com/stretchr/testify/require"
)

type testContext struct {
	context.Context
	*testing.T
	Require   *require.Assertions
	GunJSPort int
}

const defaultTestTimeout = 1 * time.Minute

func newContext(t *testing.T) (*testContext, context.CancelFunc) {
	return withTestContext(context.Background(), t)
}

func newContextWithGunJServer(t *testing.T) (*testContext, context.CancelFunc) {
	ctx, cancelFn := newContext(t)
	serverCancelFn := ctx.startGunJSServer()
	return ctx, func() {
		serverCancelFn()
		cancelFn()
	}
}

const defaultGunJSPort = 8080
const defaultRemoteGunServerURL = "https://gunjs.herokuapp.com/gun"

func withTestContext(ctx context.Context, t *testing.T) (*testContext, context.CancelFunc) {
	ctx, cancelFn := context.WithTimeout(ctx, defaultTestTimeout)
	return &testContext{
		Context:   ctx,
		T:         t,
		Require:   require.New(t),
		GunJSPort: defaultGunJSPort,
	}, cancelFn
}

func (t *testContext) debugf(format string, args ...interface{}) {
	if testing.Verbose() {
		log.Printf(format, args...)
	}
}

func (t *testContext) runJS(script string) []byte {
	cmd := exec.CommandContext(t, "node")
	_, currFile, _, _ := runtime.Caller(0)
	cmd.Dir = filepath.Dir(currFile)
	cmd.Stdin = bytes.NewReader([]byte(script))
	out, err := cmd.CombinedOutput()
	out = removeGunJSWelcome(out)
	t.Require.NoErrorf(err, "JS failure, output:\n%v", string(out))
	return out
}

func (t *testContext) runJSWithGun(script string) []byte {
	return t.runJSWithGunURL("http://127.0.0.1:"+strconv.Itoa(t.GunJSPort)+"/gun", script)
}

func (t *testContext) runJSWithGunURL(url string, script string) []byte {
	return t.runJS(`
		var Gun = require('gun')
		const gun = Gun({
			peers: ['` + url + `'],
			radisk: false
		})
		` + script)
}

func (t *testContext) startJS(script string) (*bytes.Buffer, *exec.Cmd, context.CancelFunc) {
	cmdCtx, cancelFn := context.WithCancel(t)
	cmd := exec.CommandContext(cmdCtx, "node")
	_, currFile, _, _ := runtime.Caller(0)
	cmd.Dir = filepath.Dir(currFile)
	cmd.Stdin = bytes.NewReader([]byte(script))
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	t.Require.NoError(cmd.Start())
	return &buf, cmd, cancelFn
}

func (t *testContext) startGunJSServer() context.CancelFunc {
	// If we're logging, use a proxy
	port := t.GunJSPort
	if testing.Verbose() {
		t.startGunWebSocketProxyLogger(port, "ws://127.0.0.1:"+strconv.Itoa(port+1)+"/gun")
		port++
	}
	// Remove entire data folder first just in case
	t.Require.NoError(os.RemoveAll("radata-server"))
	_, cmd, cancelFn := t.startJS(`
		var Gun = require('gun')
		const server = require('http').createServer().listen(` + strconv.Itoa(port) + `)
		const gun = Gun({web: server, file: 'radata-server'})
	`)
	return func() {
		cancelFn()
		cmd.Wait()
		// Remove the data folder at the end
		os.RemoveAll("radata-server")
	}
}

func (t *testContext) prepareRemoteGunServer(origURL string) (newURL string) {
	// If we're verbose, use proxy, otherwise just use orig
	if !testing.Verbose() {
		return origURL
	}
	origURL = strings.Replace(origURL, "http://", "ws://", 1)
	origURL = strings.Replace(origURL, "https://", "wss://", 1)
	t.startGunWebSocketProxyLogger(t.GunJSPort, origURL)
	return "http://127.0.0.1:" + strconv.Itoa(t.GunJSPort) + "/gun"
}

func (t *testContext) newGunConnectedToGunJS() *gun.Gun {
	return t.newGunConnectedToGunServer("http://127.0.0.1:" + strconv.Itoa(t.GunJSPort) + "/gun")
}

func (t *testContext) newGunConnectedToGunServer(url string) *gun.Gun {
	config := gun.Config{
		PeerURLs:         []string{url},
		PeerErrorHandler: func(errPeer *gun.ErrPeer) { t.debugf("Got peer error: %v", errPeer) },
	}
	g, err := gun.New(t, config)
	t.Require.NoError(err)
	return g
}
