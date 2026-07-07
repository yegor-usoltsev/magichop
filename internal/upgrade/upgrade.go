package upgrade

import (
	"context"
	"fmt"
)

type Options struct {
	Check   bool
	Version string
	Yes     bool
}

func Run(_ context.Context, opts Options) error {
	if opts.Check {
		fmt.Println("upgrade checks use GitHub Releases; no release source configured in this build")
		return nil
	}
	return fmt.Errorf("upgrade replacement is not configured for this development build")
}
