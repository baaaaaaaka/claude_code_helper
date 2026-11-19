package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/term"
)

const ttyRelayTestChild = "-test.run=^TestTTYRelayIntegrationChild$"

func TestTTYRelayIntegrationChild(t *testing.T) {
	switch os.Getenv("CLP_TEST_TTY_RELAY") {
	case "direct", "resize", "fallback":
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		exe, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		args := []string{exe, ttyRelayTestChild, "--"}
		stdout := &synchronizedLimitedBuffer{max: maxOutputCaptureBytes}
		if os.Getenv("CLP_TEST_TTY_RELAY") == "resize" {
			env := appendEnvOverrides(os.Environ(), []string{"CLP_TEST_TTY_RELAY=inner"})
			err = runTargetOnceWithCapturedTTYOutput(ctx, args, env, nil, nil, stdout, io.Discard, runTargetOptions{})
		} else if os.Getenv("CLP_TEST_TTY_RELAY") == "fallback" {
			t.Setenv("CLP_TEST_TTY_RELAY", "inner")
			args = append(args, "--dangerously-skip-permissions")
			err = runTargetWithFallbackWithOptions(ctx, args, "", nil, nil, nil, runTargetOptions{PreserveTTY: true, YoloEnabled: true})
		} else {
			t.Setenv("CLP_TEST_TTY_RELAY", "inner")
			err = runTargetWithFallbackWithOptions(ctx, args, "", nil, nil, nil, runTargetOptions{PreserveTTY: true})
		}
		if err != nil {
			t.Fatal(err)
		}
	case "inner":
		if slices.Contains(os.Args, "--dangerously-skip-permissions") {
			fmt.Println("Tool permission request failed: Error: Stream closed")
			time.Sleep(5 * time.Second)
			t.Fatal("the relay did not terminate the failed attempt")
		}
		done := make(chan struct{})
		go func() {
			defer close(done)
			scanner := bufio.NewScanner(os.Stdin)
			for scanner.Scan() {
				if scanner.Text() == "quit" {
					return
				}
				fmt.Println("CLP-INPUT:" + scanner.Text())
			}
		}()
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		oldWidth, oldHeight := 0, 0
		for {
			width, height, err := term.GetSize(int(os.Stdout.Fd()))
			if err == nil && (width != oldWidth || height != oldHeight) {
				fmt.Printf("CLP-SIZE:%dx%d\n", width, height)
				oldWidth, oldHeight = width, height
			}
			select {
			case <-ticker.C:
			case <-done:
				return
			}
		}
	}
}

func TestCapturedTTYResizesAndReleasesInputOnFallback(t *testing.T) {
	for _, name := range []string{"direct", "resize", "fallback"} {
		t.Run(name, func(t *testing.T) {
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			env := appendEnvOverrides(os.Environ(), []string{"CLP_TEST_TTY_RELAY=" + name})
			outer, resize := startOuterTestTTY(t, []string{exe, ttyRelayTestChild}, env, 120, 40)
			output := &synchronizedLimitedBuffer{max: maxOutputCaptureBytes}
			go func() { _, _ = io.Copy(output, outer.Output()) }()
			exit := make(chan error, 1)
			go func() { exit <- outer.Wait() }()
			waitForTTYText(t, output, "CLP-SIZE:120x40")
			if name == "resize" {
				for _, dims := range [][2]int{{72, 21}, {84, 24}, {132, 43}} {
					if err := resize(dims[0], dims[1]); err != nil {
						t.Fatal(err)
					}
				}
				waitForTTYText(t, output, "CLP-SIZE:132x43")
			}
			if _, err := io.WriteString(outer.Input(), "first\r"); err != nil {
				t.Fatal(err)
			}
			waitForTTYText(t, output, "CLP-INPUT:first")
			if _, err := io.WriteString(outer.Input(), "输入🙂\r"); err != nil {
				t.Fatal(err)
			}
			waitForTTYText(t, output, "CLP-INPUT:输入🙂")
			if _, err := io.WriteString(outer.Input(), "quit\r"); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-exit:
				if err != nil {
					t.Fatalf("captured process exited with %v: %q", err, output.String())
				}
			case <-time.After(5 * time.Second):
				t.Fatalf("captured process did not exit: %q", output.String())
			}
		})
	}
}

func waitForTTYText(t *testing.T, output *synchronizedLimitedBuffer, want string) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(output.String(), want) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("waiting for %q, got %q", want, output.String())
}
