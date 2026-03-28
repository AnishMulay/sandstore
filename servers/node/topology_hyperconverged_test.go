package node

import (
	"testing"
)

// fakeTopology is a minimal TopologyProvider for testing.
type fakeTopology struct {
	addr string
}

func (f *fakeTopology) GetLeaderAddress() string {
	return f.addr
}

func TestExternalTopology(t *testing.T) {
	tests := []struct {
		name           string
		hostIP         string
		basePort       string
		nodeID         string
		innerAddr      string
		expectedResult string
	}{
		{
			name:           "Uses external host and ordinal offset two",
			hostIP:         "10.0.0.5",
			basePort:       "9080",
			nodeID:         "sandstore-hyperconverged-2",
			innerAddr:      "sandstore-hyperconverged-2.sandstore-hyperconverged-headless:8080",
			expectedResult: "10.0.0.5:9082",
		},
		{
			name:           "Uses external host and ordinal offset zero",
			hostIP:         "10.0.0.5",
			basePort:       "9080",
			nodeID:         "sandstore-hyperconverged-0",
			innerAddr:      "sandstore-hyperconverged-0.sandstore-hyperconverged-headless:8080",
			expectedResult: "10.0.0.5:9080",
		},
		{
			name:           "Returns inner address when host is unset",
			hostIP:         "",
			basePort:       "9080",
			nodeID:         "sandstore-hyperconverged-1",
			innerAddr:      "sandstore-hyperconverged-1.sandstore-hyperconverged-headless:8080",
			expectedResult: "sandstore-hyperconverged-1.sandstore-hyperconverged-headless:8080",
		},
		{
			name:           "Returns inner address when external base port is unset",
			hostIP:         "10.0.0.5",
			basePort:       "",
			nodeID:         "sandstore-hyperconverged-1",
			innerAddr:      "sandstore-hyperconverged-1.sandstore-hyperconverged-headless:8080",
			expectedResult: "sandstore-hyperconverged-1.sandstore-hyperconverged-headless:8080",
		},
		{
			name:           "Defaults ordinal to zero when node ID has no numeric suffix",
			hostIP:         "10.0.0.5",
			basePort:       "9080",
			nodeID:         "sandstore",
			innerAddr:      "sandstore-hyperconverged-0.sandstore-hyperconverged-headless:8080",
			expectedResult: "10.0.0.5:9080",
		},
		{
			name:           "Returns empty when inner topology has no leader",
			hostIP:         "10.0.0.5",
			basePort:       "9080",
			nodeID:         "sandstore-hyperconverged-1",
			innerAddr:      "",
			expectedResult: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inner := &fakeTopology{addr: tt.innerAddr}
			wrapper := &externalTopology{
				inner:            inner,
				advertiseHost:    tt.hostIP,
				externalBasePort: tt.basePort,
				nodeID:           tt.nodeID,
			}

			got := wrapper.GetLeaderAddress()
			if got != tt.expectedResult {
				t.Errorf("GetLeaderAddress() = %q, want %q", got, tt.expectedResult)
			}
		})
	}
}
