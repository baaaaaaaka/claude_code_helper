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
