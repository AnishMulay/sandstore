package node

import (
	"os"
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
			name:           "Test 1: HOST_IP and EXTERNAL_BASE_PORT set, ordinal 2",
			hostIP:         "10.0.0.5",
			basePort:       "9080",
			nodeID:         "sandstore-2",
			innerAddr:      "sandstore-2.sandstore-headless:8080",
			expectedResult: "10.0.0.5:9082",
		},
		{
			name:           "Test 2: HOST_IP and EXTERNAL_BASE_PORT set, ordinal 0",
			hostIP:         "10.0.0.5",
			basePort:       "9080",
			nodeID:         "sandstore-0",
			innerAddr:      "sandstore-0.sandstore-headless:8080",
			expectedResult: "10.0.0.5:9080",
		},
		{
			name:           "Test 3: HOST_IP unset, returns inner address unchanged",
			hostIP:         "",
			basePort:       "9080",
			nodeID:         "sandstore-1",
			innerAddr:      "sandstore-1.sandstore-headless:8080",
			expectedResult: "sandstore-1.sandstore-headless:8080",
		},
		{
			name:           "Test 4: EXTERNAL_BASE_PORT unset, returns inner address unchanged",
			hostIP:         "10.0.0.5",
			basePort:       "",
			nodeID:         "sandstore-1",
			innerAddr:      "sandstore-1.sandstore-headless:8080",
			expectedResult: "sandstore-1.sandstore-headless:8080",
		},
		{
			name:           "Test 5: NODE_ID has no numeric suffix, ordinal defaults to 0",
			hostIP:         "10.0.0.5",
			basePort:       "9080",
			nodeID:         "sandstore",
			innerAddr:      "sandstore-0.sandstore-headless:8080",
			expectedResult: "10.0.0.5:9080",
		},
		{
			name:           "Test 6: inner returns empty string (no leader), wrapper returns empty",
			hostIP:         "10.0.0.5",
			basePort:       "9080",
			nodeID:         "sandstore-1",
			innerAddr:      "",
			expectedResult: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set environment variables with cleanup to avoid leaking between tests.
			if tt.hostIP != "" {
				t.Setenv("HOST_IP", tt.hostIP)
			} else {
				os.Unsetenv("HOST_IP")
				t.Cleanup(func() { os.Unsetenv("HOST_IP") })
			}
			if tt.basePort != "" {
				t.Setenv("EXTERNAL_BASE_PORT", tt.basePort)
			} else {
				os.Unsetenv("EXTERNAL_BASE_PORT")
				t.Cleanup(func() { os.Unsetenv("EXTERNAL_BASE_PORT") })
			}
			if tt.nodeID != "" {
				t.Setenv("NODE_ID", tt.nodeID)
			} else {
				os.Unsetenv("NODE_ID")
				t.Cleanup(func() { os.Unsetenv("NODE_ID") })
			}

			inner := &fakeTopology{addr: tt.innerAddr}
			wrapper := &externalTopology{inner: inner}

			got := wrapper.GetLeaderAddress()
			if got != tt.expectedResult {
				t.Errorf("GetLeaderAddress() = %q, want %q", got, tt.expectedResult)
			}
		})
	}
}
