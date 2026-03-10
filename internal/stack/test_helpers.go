package stack

// NewStackForTest returns a minimal Stack for tests without SSH.
func NewStackForTest(httpPort, socksPort int) *Stack {
	return &Stack{
		HTTPPort:  httpPort,
		SocksPort: socksPort,
		fatalCh:   make(chan error),
		stopCh:    make(chan struct{}),
	}
}

// NewStackWithFatalForTest returns a minimal Stack with a caller-provided fatal channel.
func NewStackWithFatalForTest(httpPort, socksPort int, fatalCh chan error) *Stack {
	if fatalCh == nil {
		fatalCh = make(chan error)
	}
	return &Stack{
		HTTPPort:  httpPort,
		SocksPort: socksPort,
		fatalCh:   fatalCh,
		stopCh:    make(chan struct{}),
	}
}
