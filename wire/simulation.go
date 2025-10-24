package wire

import (
	"math"
	"math/rand"
	"time"
)

// SimulationConfig controls the realism of BLE behavior
// Default: ~98.4% reliability with realistic timing
type SimulationConfig struct {
	// MTU (Maximum Transmission Unit) - BLE packet size limits
	MinMTU int // Default: 23 bytes (BLE 4.0 minimum)
	MaxMTU int // Default: 512 bytes (BLE 5.0+ maximum)
	DefaultMTU int // Default: 185 bytes (common negotiated value)

	// Connection timing (in milliseconds)
	MinConnectionDelay int // Default: 30ms
	MaxConnectionDelay int // Default: 100ms
	ConnectionFailureRate float64 // Default: 0.016 (1.6% failure rate)

	// Discovery timing (in milliseconds)
	AdvertisingInterval int // Default: 100ms (Apple's recommended interval)
	MinDiscoveryDelay int // Default: 100ms
	MaxDiscoveryDelay int // Default: 1000ms

	// Radio characteristics
	EnableRSSI bool // Default: true
	BaseRSSI int // Default: -50 dBm (close range)
	RSSIVariance int // Default: 10 dBm (realistic fluctuation)

	// Packet loss and reliability
	PacketLossRate float64 // Default: 0.015 (1.5% packet loss)
	MaxRetries int // Default: 3 retries
	RetryDelay int // Default: 50ms between retries

	// Connection states
	EnableConnectionStates bool // Default: true
	DisconnectingDelay int // Default: 20ms

	// Deterministic mode for testing
	Deterministic bool // Default: false (use for reproducible scenarios)
	Seed int64 // Random seed when Deterministic=true
}

// DefaultSimulationConfig returns realistic BLE simulation parameters
// Yields approximately 98.4% success rate:
// - 98.4% packets succeed on first try
// - 1.5% packets lost but succeed on retry (1-3 retries)
// - ~0.1% total failure after all retries
func DefaultSimulationConfig() *SimulationConfig {
	return &SimulationConfig{
		MinMTU: 23,
		MaxMTU: 512,
		DefaultMTU: 185,

		MinConnectionDelay: 30,
		MaxConnectionDelay: 100,
		ConnectionFailureRate: 0.016, // 1.6% connection failures

		AdvertisingInterval: 100,
		MinDiscoveryDelay: 100,
		MaxDiscoveryDelay: 1000,

		EnableRSSI: true,
		BaseRSSI: -50,
		RSSIVariance: 10,

		PacketLossRate: 0.015, // 1.5% packet loss
		MaxRetries: 3,
		RetryDelay: 50,

		EnableConnectionStates: true,
		DisconnectingDelay: 20,

		Deterministic: false,
		Seed: 0,
	}
}

// PerfectSimulationConfig returns 100% reliable config for testing
func PerfectSimulationConfig() *SimulationConfig {
	cfg := DefaultSimulationConfig()
	cfg.MinConnectionDelay = 0
	cfg.MaxConnectionDelay = 0
	cfg.ConnectionFailureRate = 0
	cfg.MinDiscoveryDelay = 0
	cfg.MaxDiscoveryDelay = 0
	cfg.PacketLossRate = 0
	cfg.Deterministic = true
	return cfg
}

// Simulator handles realistic BLE behavior simulation
type Simulator struct {
	config *SimulationConfig
	rng *rand.Rand
}

// NewSimulator creates a new BLE simulator
func NewSimulator(config *SimulationConfig) *Simulator {
	if config == nil {
		config = DefaultSimulationConfig()
	}

	var rng *rand.Rand
	if config.Deterministic {
		rng = rand.New(rand.NewSource(config.Seed))
	} else {
		rng = rand.New(rand.NewSource(time.Now().UnixNano()))
	}

	return &Simulator{
		config: config,
		rng: rng,
	}
}

// ShouldConnectionSucceed returns true if connection should succeed
func (s *Simulator) ShouldConnectionSucceed() bool {
	return s.rng.Float64() >= s.config.ConnectionFailureRate
}

// ConnectionDelay returns realistic connection delay
func (s *Simulator) ConnectionDelay() time.Duration {
	if s.config.MinConnectionDelay == s.config.MaxConnectionDelay {
		return time.Duration(s.config.MinConnectionDelay) * time.Millisecond
	}
	delay := s.config.MinConnectionDelay +
		s.rng.Intn(s.config.MaxConnectionDelay - s.config.MinConnectionDelay)
	return time.Duration(delay) * time.Millisecond
}

// DiscoveryDelay returns realistic discovery delay
func (s *Simulator) DiscoveryDelay() time.Duration {
	if s.config.MinDiscoveryDelay == s.config.MaxDiscoveryDelay {
		return time.Duration(s.config.MinDiscoveryDelay) * time.Millisecond
	}
	delay := s.config.MinDiscoveryDelay +
		s.rng.Intn(s.config.MaxDiscoveryDelay - s.config.MinDiscoveryDelay)
	return time.Duration(delay) * time.Millisecond
}

// ShouldPacketSucceed returns true if packet transmission should succeed
func (s *Simulator) ShouldPacketSucceed() bool {
	return s.rng.Float64() >= s.config.PacketLossRate
}

// GenerateRSSI returns realistic RSSI value with variance
// distance: approximate distance in meters (1-10)
func (s *Simulator) GenerateRSSI(distance float64) int {
	if !s.config.EnableRSSI {
		return s.config.BaseRSSI
	}

	// Free space path loss formula (simplified)
	// RSSI decreases by ~20dB per 10x distance
	pathLoss := 20 * math.Log10(distance)
	rssi := float64(s.config.BaseRSSI) - pathLoss

	// Add random variance (realistic radio interference)
	variance := s.rng.Intn(s.config.RSSIVariance*2) - s.config.RSSIVariance
	rssi += float64(variance)

	// Clamp to realistic BLE range (-100 to -20 dBm)
	if rssi < -100 {
		rssi = -100
	} else if rssi > -20 {
		rssi = -20
	}

	return int(rssi)
}

// NegotiatedMTU returns the MTU after negotiation
// Both devices propose their max MTU, the minimum is selected
func (s *Simulator) NegotiatedMTU(device1MTU, device2MTU int) int {
	mtu := device1MTU
	if device2MTU < mtu {
		mtu = device2MTU
	}

	// Clamp to valid range
	if mtu < s.config.MinMTU {
		mtu = s.config.MinMTU
	} else if mtu > s.config.MaxMTU {
		mtu = s.config.MaxMTU
	}

	return mtu
}

// FragmentData splits data into MTU-sized chunks
// Returns array of chunks that fit within MTU
func (s *Simulator) FragmentData(data []byte, mtu int) [][]byte {
	if len(data) <= mtu {
		return [][]byte{data}
	}

	var chunks [][]byte
	for i := 0; i < len(data); i += mtu {
		end := i + mtu
		if end > len(data) {
			end = len(data)
		}
		chunks = append(chunks, data[i:end])
	}

	return chunks
}

// ConnectionState represents BLE connection states
type ConnectionState int

const (
	StateDisconnected ConnectionState = iota
	StateConnecting
	StateConnected
	StateDisconnecting
)

func (s ConnectionState) String() string {
	switch s {
	case StateDisconnected:
		return "disconnected"
	case StateConnecting:
		return "connecting"
	case StateConnected:
		return "connected"
	case StateDisconnecting:
		return "disconnecting"
	default:
		return "unknown"
	}
}

// DisconnectDelay returns delay for disconnection
func (s *Simulator) DisconnectDelay() time.Duration {
	return time.Duration(s.config.DisconnectingDelay) * time.Millisecond
}
