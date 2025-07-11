/*
   Copyright The containerd Authors.

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

package dockercompat

import (
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/docker/go-connections/nat"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/opencontainers/runtime-spec/specs-go"
	"gotest.tools/v3/assert"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/containers"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/go-cni"

	"github.com/containerd/nerdctl/v2/pkg/healthcheck"
	"github.com/containerd/nerdctl/v2/pkg/inspecttypes/native"
	"github.com/containerd/nerdctl/v2/pkg/internal/filesystem"
	"github.com/containerd/nerdctl/v2/pkg/labels"
)

func TestContainerFromNative(t *testing.T) {
	tempStateDir, err := os.MkdirTemp(t.TempDir(), "rw")
	if err != nil {
		t.Fatal(err)
	}
	filesystem.WriteFile(filepath.Join(tempStateDir, "resolv.conf"), []byte(""), 0644)
	defer os.RemoveAll(tempStateDir)

	hc := &healthcheck.Healthcheck{
		Test:        []string{"CMD-SHELL", "curl -f http://localhost || exit 1"},
		Interval:    time.Second * 30,
		Timeout:     time.Second * 5,
		Retries:     3,
		StartPeriod: time.Second * 10,
	}
	hcJSON, err := hc.ToJSONString()
	assert.NilError(t, err)

	testcase := []struct {
		name     string
		n        *native.Container
		expected *Container
	}{
		// nerdctl container, mount /mnt/foo:/mnt/foo:rw,rslave; ResolvConfPath; hostname
		{
			name: "container from nerdctl",
			n: &native.Container{
				Container: containers.Container{
					Labels: map[string]string{
						"nerdctl/mounts":    "[{\"Type\":\"bind\",\"Source\":\"/mnt/foo\",\"Destination\":\"/mnt/foo\",\"Mode\":\"rshared,rw\",\"RW\":true,\"Propagation\":\"rshared\"}]",
						"nerdctl/state-dir": tempStateDir,
						"nerdctl/hostname":  "host1",
						"nerdctl/user":      "test-user",
					},
				},
				Spec: &specs.Spec{
					Process: &specs.Process{
						Env: []string{"/some/path"},
					},
				},
				Process: &native.Process{
					Pid: 10000,
					Status: containerd.Status{
						Status: "running",
					},
				},
			},
			expected: &Container{
				Created:        "0001-01-01T00:00:00Z",
				Platform:       runtime.GOOS,
				ResolvConfPath: filepath.Join(tempStateDir, "resolv.conf"),
				State: &ContainerState{
					Status:     "running",
					Running:    true,
					Pid:        10000,
					FinishedAt: "",
				},
				HostConfig: &HostConfig{
					PortBindings: nat.PortMap{},
					GroupAdd:     []string{},
					LogConfig: loggerLogConfig{
						Driver: "json-file",
						Opts:   map[string]string{},
					},
					UTSMode:            "host",
					Tmpfs:              map[string]string{},
					LinuxBlkioSettings: getDefaultLinuxBlkioSettings(),
				},
				Mounts: []MountPoint{
					{
						Type:        "bind",
						Source:      "/mnt/foo",
						Destination: "/mnt/foo",
						Mode:        "rshared,rw",
						RW:          true,
						Propagation: "rshared",
					},
				},
				Config: &Config{
					Labels: map[string]string{
						"nerdctl/mounts":    "[{\"Type\":\"bind\",\"Source\":\"/mnt/foo\",\"Destination\":\"/mnt/foo\",\"Mode\":\"rshared,rw\",\"RW\":true,\"Propagation\":\"rshared\"}]",
						"nerdctl/state-dir": tempStateDir,
						"nerdctl/hostname":  "host1",
						"nerdctl/user":      "test-user",
					},
					Hostname: "host1",
					Env:      []string{"/some/path"},
					User:     "test-user",
				},
				NetworkSettings: &NetworkSettings{
					Ports:    &nat.PortMap{},
					Networks: map[string]*NetworkEndpointSettings{},
				},
			},
		},
		// cri container, mount /mnt/foo:/mnt/foo:rw,rslave; mount resolv.conf and hostname; internal sysfs mount
		{
			name: "container from cri",
			n: &native.Container{
				Container: containers.Container{},
				Spec: &specs.Spec{
					Mounts: []specs.Mount{
						{
							Destination: "/etc/resolv.conf",
							Type:        "bind",
							Source:      "/mock-sandbox-dir/resolv.conf",
							Options:     []string{"rbind", "rprivate", "rw"},
						},
						{
							Destination: "/etc/hostname",
							Type:        "bind",
							Source:      "/mock-sandbox-dir/hostname",
							Options:     []string{"rbind", "rprivate", "rw"},
						},
						{
							Destination: "/mnt/foo",
							Type:        "bind",
							Source:      "/mnt/foo",
							Options:     []string{"rbind", "rslave", "rw"},
						},
						{
							Destination: "/sys",
							Type:        "sysfs",
							Source:      "sysfs",
							Options:     []string{"nosuid", "noexec", "nodev", "ro"},
						},
						{
							Destination: "/etc/hosts",
							Type:        "bind",
							Source:      "/mock-sandbox-dir/hosts",
							Options:     []string{"bind", "rprivate", "rw"},
						},
					},
				},
				Process: &native.Process{
					Pid: 10000,
					Status: containerd.Status{
						Status: "running",
					},
				},
			},
			expected: &Container{
				Created:        "0001-01-01T00:00:00Z",
				Platform:       runtime.GOOS,
				ResolvConfPath: "/mock-sandbox-dir/resolv.conf",
				HostnamePath:   "/mock-sandbox-dir/hostname",
				HostsPath:      "/mock-sandbox-dir/hosts",
				State: &ContainerState{
					Status:     "running",
					Running:    true,
					Pid:        10000,
					FinishedAt: "",
				},
				HostConfig: &HostConfig{
					PortBindings: nat.PortMap{},
					GroupAdd:     []string{},
					LogConfig: loggerLogConfig{
						Driver: "json-file",
						Opts:   map[string]string{},
					},
					UTSMode:            "host",
					Tmpfs:              map[string]string{},
					LinuxBlkioSettings: getDefaultLinuxBlkioSettings(),
				},
				Mounts: []MountPoint{
					{
						Type:        "bind",
						Source:      "/mock-sandbox-dir/resolv.conf",
						Destination: "/etc/resolv.conf",
						Mode:        "rbind,rprivate,rw",
						RW:          true,
						Propagation: "rprivate",
					},
					{
						Type:        "bind",
						Source:      "/mock-sandbox-dir/hostname",
						Destination: "/etc/hostname",
						Mode:        "rbind,rprivate,rw",
						RW:          true,
						Propagation: "rprivate",
					},
					{
						Type:        "bind",
						Source:      "/mnt/foo",
						Destination: "/mnt/foo",
						Mode:        "rbind,rslave,rw",
						RW:          true,
						Propagation: "rslave",
					},
					{
						Type:        "bind",
						Source:      "/mock-sandbox-dir/hosts",
						Destination: "/etc/hosts",
						Mode:        "bind,rprivate,rw",
						RW:          true,
						Propagation: "rprivate",
					},
					// ignore sysfs mountpoint
				},
				Config: &Config{},
				NetworkSettings: &NetworkSettings{
					Ports:    &nat.PortMap{},
					Networks: map[string]*NetworkEndpointSettings{},
				},
			},
		},
		// ctr container, mount /mnt/foo:/mnt/foo:rw,rslave; internal sysfs mount; hostname
		{
			name: "container from ctr",
			n: &native.Container{
				Container: containers.Container{},
				Spec: &specs.Spec{
					Hostname: "host1",
					Mounts: []specs.Mount{
						{
							Destination: "/mnt/foo",
							Type:        "bind",
							Source:      "/mnt/foo",
							Options:     []string{"rbind", "rslave", "rw"},
						},
						{
							Destination: "/sys",
							Type:        "sysfs",
							Source:      "sysfs",
							Options:     []string{"nosuid", "noexec", "nodev", "ro"},
						},
					},
				},
				Process: &native.Process{
					Pid: 10000,
					Status: containerd.Status{
						Status: "running",
					},
				},
			},
			expected: &Container{
				Created:  "0001-01-01T00:00:00Z",
				Platform: runtime.GOOS,
				State: &ContainerState{
					Status:     "running",
					Running:    true,
					Pid:        10000,
					FinishedAt: "",
				},
				HostConfig: &HostConfig{
					PortBindings: nat.PortMap{},
					GroupAdd:     []string{},
					LogConfig: loggerLogConfig{
						Driver: "json-file",
						Opts:   map[string]string{},
					},
					UTSMode:            "host",
					Tmpfs:              map[string]string{},
					LinuxBlkioSettings: getDefaultLinuxBlkioSettings(),
				},
				Mounts: []MountPoint{
					{
						Type:        "bind",
						Source:      "/mnt/foo",
						Destination: "/mnt/foo",
						Mode:        "rbind,rslave,rw",
						RW:          true,
						Propagation: "rslave",
					},
					// ignore sysfs mountpoint
				},
				Config: &Config{
					Hostname: "host1",
				},
				NetworkSettings: &NetworkSettings{
					Ports:    &nat.PortMap{},
					Networks: map[string]*NetworkEndpointSettings{},
				},
			},
		},
		{
			name: "container with healthcheck label",
			n: &native.Container{
				Container: containers.Container{
					Labels: map[string]string{
						labels.HealthCheck: hcJSON,
					},
				},
				Spec: &specs.Spec{},
				Process: &native.Process{
					Status: containerd.Status{
						Status: "running",
					},
				},
			},
			expected: &Container{
				Created:  "0001-01-01T00:00:00Z",
				Platform: runtime.GOOS,
				Mounts:   []MountPoint{},
				State: &ContainerState{
					Status:     "running",
					Running:    true,
					Pid:        0,
					FinishedAt: "",
				},
				HostConfig: &HostConfig{
					LogConfig:          loggerLogConfig{Driver: "json-file", Opts: map[string]string{}},
					PortBindings:       nat.PortMap{},
					GroupAdd:           []string{},
					Tmpfs:              map[string]string{},
					UTSMode:            "host",
					LinuxBlkioSettings: getDefaultLinuxBlkioSettings(),
				},
				NetworkSettings: &NetworkSettings{
					Ports:    &nat.PortMap{},
					Networks: map[string]*NetworkEndpointSettings{},
				},
				Config: &Config{
					Labels: map[string]string{
						labels.HealthCheck: hcJSON,
					},
					Healthcheck: hc,
				},
			},
		},
	}

	for _, tc := range testcase {
		t.Run(tc.name, func(tt *testing.T) {
			d, _ := ContainerFromNative(tc.n)
			assert.DeepEqual(tt, d, tc.expected)
		})
	}
}

func TestNetworkSettingsFromNative(t *testing.T) {
	tempStateDir, err := os.MkdirTemp(t.TempDir(), "rw")
	if err != nil {
		t.Fatal(err)
	}
	filesystem.WriteFile(filepath.Join(tempStateDir, "resolv.conf"), []byte(""), 0644)
	defer os.RemoveAll(tempStateDir)

	testcase := []struct {
		name     string
		n        *native.NetNS
		s        *specs.Spec
		expected *NetworkSettings
	}{
		// Given null native.NetNS, Return initialized NetworkSettings
		//    UseCase: Inspect a Stopped Container
		{
			name: "Given Null NetNS, Return initialized NetworkSettings",
			n:    nil,
			s:    &specs.Spec{},
			expected: &NetworkSettings{
				Ports:    &nat.PortMap{},
				Networks: map[string]*NetworkEndpointSettings{},
			},
		},
		// Given native.NetNS with single Interface with Port Annotations, Return populated NetworkSettings
		//   UseCase: Inspect a Running Container with published ports
		{
			name: "Given NetNS with single Interface with Port Annotation, Return populated NetworkSettings",
			n: &native.NetNS{
				Interfaces: []native.NetInterface{
					{
						Interface: net.Interface{
							Index: 1,
							MTU:   1500,
							Name:  "eth0.100",
							Flags: net.FlagUp,
						},
						HardwareAddr: "xx:xx:xx:xx:xx:xx",
						Flags:        []string{},
						Addrs:        []string{"10.0.4.30/24"},
					},
				},
				PortMappings: []cni.PortMapping{
					{
						HostPort:      8075,
						ContainerPort: 77,
						Protocol:      "tcp",
						HostIP:        "127.0.0.1",
					},
				},
			},
			s: &specs.Spec{
				Annotations: map[string]string{},
			},
			expected: &NetworkSettings{
				Ports: &nat.PortMap{
					nat.Port("77/tcp"): []nat.PortBinding{
						{
							HostIP:   "127.0.0.1",
							HostPort: "8075",
						},
					},
				},
				Networks: map[string]*NetworkEndpointSettings{
					"unknown-eth0.100": {
						IPAddress:   "10.0.4.30",
						IPPrefixLen: 24,
						MacAddress:  "xx:xx:xx:xx:xx:xx",
					},
				},
			},
		},
		// Given native.NetNS with single Interface without Port Annotations, Return valid NetworkSettings w/ empty Ports
		//   UseCase: Inspect a Running Container without published ports
		{
			name: "Given NetNS with single Interface without Port Annotations, Return valid NetworkSettings w/ empty Ports",
			n: &native.NetNS{
				Interfaces: []native.NetInterface{
					{
						Interface: net.Interface{
							Index: 1,
							MTU:   1500,
							Name:  "eth0.100",
							Flags: net.FlagUp,
						},
						HardwareAddr: "xx:xx:xx:xx:xx:xx",
						Flags:        []string{},
						Addrs:        []string{"10.0.4.30/24"},
					},
				},
			},
			s: &specs.Spec{
				Annotations: map[string]string{},
			},
			expected: &NetworkSettings{
				Ports: &nat.PortMap{},
				Networks: map[string]*NetworkEndpointSettings{
					"unknown-eth0.100": {
						IPAddress:   "10.0.4.30",
						IPPrefixLen: 24,
						MacAddress:  "xx:xx:xx:xx:xx:xx",
					},
				},
			},
		},
	}

	for _, tc := range testcase {
		t.Run(tc.name, func(tt *testing.T) {
			d, _ := networkSettingsFromNative(tc.n, tc.s)
			assert.DeepEqual(tt, d, tc.expected)
		})
	}
}

func TestCpuSettingsFromNative(t *testing.T) {
	// Helper function to create uint64 pointer
	uint64Ptr := func(i uint64) *uint64 {
		return &i
	}

	int64Ptr := func(i int64) *int64 {
		return &i
	}

	testcases := []struct {
		name     string
		spec     *specs.Spec
		expected *CPUSettings
	}{
		{
			name:     "Test with empty spec",
			spec:     &specs.Spec{},
			expected: &CPUSettings{},
		},
		{
			name: "Full CPU Settings",
			spec: &specs.Spec{
				Linux: &specs.Linux{
					Resources: &specs.LinuxResources{
						CPU: &specs.LinuxCPU{
							Cpus:            "0-3",
							Mems:            "0-1",
							Shares:          uint64Ptr(1024),
							Quota:           int64Ptr(100000),
							Period:          uint64Ptr(100000),
							RealtimePeriod:  uint64Ptr(1000000),
							RealtimeRuntime: int64Ptr(950000),
						},
					},
				},
			},
			expected: &CPUSettings{
				CPUSetCpus:         "0-3",
				CPUSetMems:         "0-1",
				CPUShares:          1024,
				CPUQuota:           100000,
				CPUPeriod:          100000,
				CPURealtimePeriod:  1000000,
				CPURealtimeRuntime: 950000,
			},
		},
		{
			name: "Partial CPU Settings",
			spec: &specs.Spec{
				Linux: &specs.Linux{
					Resources: &specs.LinuxResources{
						CPU: &specs.LinuxCPU{
							Cpus:   "0,1",
							Shares: uint64Ptr(512),
						},
					},
				},
			},
			expected: &CPUSettings{
				CPUSetCpus: "0,1",
				CPUShares:  512,
			},
		},
		{
			name: "Zero Values Should Be Ignored",
			spec: &specs.Spec{
				Linux: &specs.Linux{
					Resources: &specs.LinuxResources{
						CPU: &specs.LinuxCPU{
							Shares:          uint64Ptr(0),
							Quota:           int64Ptr(0),
							Period:          uint64Ptr(0),
							RealtimePeriod:  uint64Ptr(0),
							RealtimeRuntime: int64Ptr(0),
						},
					},
				},
			},
			expected: &CPUSettings{},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := cpuSettingsFromNative(tc.spec)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			assert.DeepEqual(t, result, tc.expected)
		})
	}
}

func TestImageFromNative(t *testing.T) {
	t.Run("parses RepoTags/Digests and RootFS Layers", func(t *testing.T) {
		createdTime := time.Now().UTC()

		img := native.Image{
			Image: images.Image{
				Name: "myrepo/myimage:custom",
				Target: ocispec.Descriptor{
					Digest: digest.Digest("sha256:targetdigest"),
				},
			},
			ImageConfigDesc: ocispec.Descriptor{
				Digest: digest.Digest("sha256:configdigest"),
			},
			ImageConfig: ocispec.Image{
				RootFS: ocispec.RootFS{
					Type:    "layers",
					DiffIDs: []digest.Digest{"sha256:layer1", "sha256:layer2"},
				},
				History: []ocispec.History{
					{
						Created: &createdTime,
						Author:  "test-author",
						Comment: "test-comment",
					},
				},
			},
		}

		out, err := ImageFromNative(&img)
		assert.NilError(t, err)

		// ID, tags, digests
		assert.Equal(t, out.ID, "sha256:configdigest")
		assert.Equal(t, out.RepoTags[0], "myrepo/myimage:custom")
		assert.Equal(t, out.RepoDigests[0], "myrepo/myimage@sha256:targetdigest")

		// RootFS
		assert.DeepEqual(t, out.RootFS.Layers, []string{"sha256:layer1", "sha256:layer2"})

		// History
		assert.Equal(t, out.Author, "test-author")
		assert.Equal(t, out.Comment, "test-comment")
		assert.Equal(t, out.Created, createdTime.Format(time.RFC3339Nano))
	})

	t.Run("parses Healthcheck label", func(t *testing.T) {
		testcases := []struct {
			name     string
			labels   map[string]string
			expected *healthcheck.Healthcheck
		}{
			{
				name: "Valid Healthcheck Label",
				labels: map[string]string{
					labels.HealthCheck: `{
						"test": ["CMD-SHELL", "curl -f http://localhost/ || exit 1"],
						"interval": 30000000000,
						"timeout": 5000000000
					}`,
				},
				expected: &healthcheck.Healthcheck{
					Test:     []string{"CMD-SHELL", "curl -f http://localhost/ || exit 1"},
					Interval: time.Second * 30,
					Timeout:  time.Second * 5,
				},
			},
			{
				name:     "No Healthcheck Label",
				labels:   map[string]string{},
				expected: nil,
			},
		}

		for _, tc := range testcases {
			t.Run(tc.name, func(t *testing.T) {
				img := native.Image{
					ImageConfig: ocispec.Image{
						Config: ocispec.ImageConfig{
							Labels: tc.labels,
						},
					},
				}

				out, err := ImageFromNative(&img)
				assert.NilError(t, err)
				assert.DeepEqual(t, out.Config.Healthcheck, tc.expected)
			})
		}
	})
}
