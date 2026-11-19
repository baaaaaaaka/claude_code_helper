package cli

import (
	"errors"
	"testing"

	"github.com/UserExistsError/conpty"
)

func startOuterTestTTY(t *testing.T, args, env []string, width, height int) (ttyCaptureSession, func(int, int) error) {
	t.Helper()
	cpty, err := conpty.Start(windowsCommandLine(args), conpty.ConPtyEnv(env), conpty.ConPtyDimensions(width, height))
	if errors.Is(err, conpty.ErrConPtyUnsupported) {
		t.Skip("this version of Windows does not support ConPTY")
	}
	if err != nil {
		t.Fatal(err)
	}
	session := &windowsTTYCaptureSession{cpty: cpty, width: width, height: height}
	t.Cleanup(func() { _ = session.Close() })
	return session, cpty.Resize
}
