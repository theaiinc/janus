package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrPairingCodeUsed    = errors.New("pairing code already used")
)

type APIKey struct {
	Key    string `json:"-"`
	Tenant string `json:"tenant"`
}

type PairingCode struct {
	Code   string `json:"-"`
	Tenant string `json:"tenant"`
}

type Credential struct {
	Hash   string `json:"hash"`
	Tenant string `json:"tenant"`
}

type State struct {
	Keys             []Credential         `json:"keys,omitempty"`
	PairingCodes     map[string]string    `json:"pairingCodes,omitempty"`
	PairingExpiresAt map[string]time.Time `json:"pairingExpiresAt,omitempty"`
	UsedCodes        map[string]bool      `json:"usedCodes,omitempty"`
}

type Store interface {
	Load(context.Context) (State, error)
	Save(context.Context, State) error
}

type MemoryStore struct {
	mu    sync.Mutex
	state State
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{state: State{PairingCodes: make(map[string]string)}}
}

func (s *MemoryStore) Load(context.Context) (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneState(s.state), nil
}

func (s *MemoryStore) Save(_ context.Context, state State) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = cloneState(state)
	return nil
}

type FileStore struct {
	path string
	mu   sync.Mutex
}

func NewFileStore(path string) *FileStore { return &FileStore{path: path} }

func (s *FileStore) Load(context.Context) (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return State{PairingCodes: make(map[string]string)}, nil
	}
	if err != nil {
		return State{}, err
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return State{}, err
	}
	if state.PairingCodes == nil {
		state.PairingCodes = make(map[string]string)
	}
	return state, nil
}

func (s *FileStore) Save(_ context.Context, state State) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

type Manager struct {
	mu      sync.Mutex
	store   Store
	keys    map[string]string
	codes   map[string]string
	used    map[string]bool
	expires map[string]time.Time
	enabled bool
}

func New(enabled bool, keys []APIKey, codes []PairingCode, store Store) (*Manager, error) {
	if store == nil {
		store = NewMemoryStore()
	}
	state, err := store.Load(context.Background())
	if err != nil {
		return nil, err
	}
	m := &Manager{enabled: enabled, store: store, keys: make(map[string]string), codes: make(map[string]string), used: make(map[string]bool), expires: make(map[string]time.Time)}
	for _, key := range state.Keys {
		m.keys[key.Hash] = key.Tenant
	}
	for hash, tenant := range state.PairingCodes {
		m.codes[hash] = tenant
	}
	for hashValue, used := range state.UsedCodes {
		m.used[hashValue] = used
	}
	for hashValue, expires := range state.PairingExpiresAt {
		m.expires[hashValue] = expires
	}
	for _, key := range keys {
		if strings.TrimSpace(key.Key) != "" {
			m.keys[hash(key.Key)] = strings.TrimSpace(key.Tenant)
		}
	}
	for _, code := range codes {
		if strings.TrimSpace(code.Code) != "" && !m.used[hash(code.Code)] {
			m.codes[hash(code.Code)] = strings.TrimSpace(code.Tenant)
		}
	}
	return m, nil
}

func (m *Manager) Enabled() bool { return m != nil && m.enabled }

func (m *Manager) Authenticate(raw string) (string, bool) {
	if !m.Enabled() {
		return "", true
	}
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "Bearer ")
	raw = strings.TrimSpace(raw)
	m.mu.Lock()
	tenant, ok := m.keys[hash(raw)]
	m.mu.Unlock()
	return tenant, ok
}

func (m *Manager) Exchange(ctx context.Context, code string) (string, string, error) {
	if !m.Enabled() {
		return "", "", ErrInvalidCredentials
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	codeHash := hash(code)
	tenant, ok := m.codes[codeHash]
	if !ok {
		return "", "", ErrInvalidCredentials
	}
	if m.used[codeHash] {
		return "", "", ErrPairingCodeUsed
	}
	if expires, ok := m.expires[codeHash]; ok && !expires.IsZero() && time.Now().After(expires) {
		delete(m.codes, codeHash)
		return "", "", ErrInvalidCredentials
	}
	key, err := randomToken()
	if err != nil {
		return "", "", err
	}
	m.keys[hash(key)] = tenant
	m.used[codeHash] = true
	state := State{PairingCodes: make(map[string]string), PairingExpiresAt: make(map[string]time.Time), UsedCodes: make(map[string]bool)}
	for hashValue, keyTenant := range m.keys {
		state.Keys = append(state.Keys, Credential{Hash: hashValue, Tenant: keyTenant})
	}
	for hashValue, codeTenant := range m.codes {
		state.PairingCodes[hashValue] = codeTenant
		if expires, ok := m.expires[hashValue]; ok {
			state.PairingExpiresAt[hashValue] = expires
		}
		if m.used[hashValue] {
			state.UsedCodes[hashValue] = true
		}
	}
	if err := m.store.Save(ctx, state); err != nil {
		delete(m.keys, hash(key))
		delete(m.used, codeHash)
		return "", "", err
	}
	return key, tenant, nil
}

// GeneratePairingCode creates a short-lived, single-use human-readable code.
func (m *Manager) GeneratePairingCode(ctx context.Context, tenant string, ttl time.Duration) (string, error) {
	if !m.Enabled() {
		return "", ErrInvalidCredentials
	}
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	raw, err := randomPairingCode()
	if err != nil {
		return "", err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	codeHash := hash(raw)
	m.codes[codeHash] = strings.TrimSpace(tenant)
	m.expires[codeHash] = time.Now().Add(ttl)
	state := State{PairingCodes: make(map[string]string), PairingExpiresAt: make(map[string]time.Time), UsedCodes: make(map[string]bool)}
	for hashValue, keyTenant := range m.keys {
		state.Keys = append(state.Keys, Credential{Hash: hashValue, Tenant: keyTenant})
	}
	for hashValue, codeTenant := range m.codes {
		state.PairingCodes[hashValue] = codeTenant
		state.PairingExpiresAt[hashValue] = m.expires[hashValue]
	}
	for hashValue, used := range m.used {
		state.UsedCodes[hashValue] = used
	}
	if err := m.store.Save(ctx, state); err != nil {
		delete(m.codes, codeHash)
		delete(m.expires, codeHash)
		return "", err
	}
	return raw, nil
}

func hash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func randomToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "janus_" + hex.EncodeToString(buf), nil
}

func randomPairingCode() (string, error) {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	for i := range buf {
		buf[i] = alphabet[int(buf[i])%len(alphabet)]
	}
	return string(buf[:4]) + "-" + string(buf[4:]), nil
}

func cloneState(in State) State {
	out := State{Keys: append([]Credential(nil), in.Keys...), PairingCodes: make(map[string]string, len(in.PairingCodes)), PairingExpiresAt: make(map[string]time.Time, len(in.PairingExpiresAt)), UsedCodes: make(map[string]bool, len(in.UsedCodes))}
	for key, value := range in.PairingCodes {
		out.PairingCodes[key] = value
	}
	for key, value := range in.UsedCodes {
		out.UsedCodes[key] = value
	}
	for key, value := range in.PairingExpiresAt {
		out.PairingExpiresAt[key] = value
	}
	return out
}
