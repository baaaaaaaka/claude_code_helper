//go:build !darwin

package cli

import "io"

func adhocCodesign(_ string, _ io.Writer) error {
	return nil
}
