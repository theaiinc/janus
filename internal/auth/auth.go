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
	ErrDaemonCredential   = errors.New("daemon credential is required")
	ErrDaemonIdentity     = errors.New("daemon identity mismatch")
	ErrNamespaceTenant    = errors.New("daemon credential is not authorized for namespace")
	ErrNamespaceOwned     = errors.New("namespace is owned by another daemon")
)

type APIKey struct {
	Key      string `json:"-"`
	Tenant   string `json:"tenant"`
	Scope    string `json:"scope,omitempty"`
	Identity string `json:"identity,omitempty"`
}

type PairingCode struct {
	Code     string `json:"-"`
	Tenant   string `json:"tenant"`
	Scope    string `json:"scope,omitempty"`
	Identity string `json:"identity,omitempty"`
}

type Credential struct {
	Hash     string `json:"hash"`
	Tenant   string `json:"tenant"`
	Scope    string `json:"scope,omitempty"`
	Identity string `json:"identity,omitempty"`
}

type State struct {
	Keys               []Credential          `json:"keys,omitempty"`
	PairingCodes       map[string]string     `json:"pairingCodes,omitempty"`
	PairingCredentials map[string]Credential `json:"pairingCredentials,omitempty"`
	PairingExpiresAt   map[string]time.Time  `json:"pairingExpiresAt,omitempty"`
	UsedCodes          map[string]bool       `json:"usedCodes,omitempty"`
	NamespaceOwners    map[string]string     `json:"namespaceOwners,omitempty"`
	PrivateNamespaces  map[string]bool       `json:"privateNamespaces,omitempty"`
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
	keys    map[string]Credential
	codes   map[string]Credential
	used    map[string]bool
	expires map[string]time.Time
	owners  map[string]string
	private map[string]bool
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
	m := &Manager{enabled: enabled, store: store, keys: make(map[string]Credential), codes: make(map[string]Credential), used: make(map[string]bool), expires: make(map[string]time.Time), owners: make(map[string]string), private: make(map[string]bool)}
	for _, key := range state.Keys {
		m.keys[key.Hash] = key
	}
	for hash, tenant := range state.PairingCodes {
		m.codes[hash] = Credential{Tenant: tenant}
	}
	for hash, credential := range state.PairingCredentials {
		m.codes[hash] = credential
	}
	for hashValue, used := range state.UsedCodes {
		m.used[hashValue] = used
	}
	for hashValue, expires := range state.PairingExpiresAt {
		m.expires[hashValue] = expires
	}
	for namespace, identity := range state.NamespaceOwners {
		m.owners[strings.ToLower(strings.TrimSpace(namespace))] = strings.TrimSpace(identity)
	}
	for namespace, private := range state.PrivateNamespaces {
		m.private[strings.ToLower(strings.TrimSpace(namespace))] = private
	}
	for _, key := range keys {
		if strings.TrimSpace(key.Key) != "" {
			m.keys[hash(key.Key)] = Credential{
				Tenant: strings.TrimSpace(key.Tenant), Scope: strings.TrimSpace(key.Scope), Identity: strings.TrimSpace(key.Identity),
			}
		}
	}
	for _, code := range codes {
		if strings.TrimSpace(code.Code) != "" && !m.used[hash(code.Code)] {
			m.codes[hash(code.Code)] = Credential{
				Tenant: strings.TrimSpace(code.Tenant), Scope: strings.TrimSpace(code.Scope), Identity: strings.TrimSpace(code.Identity),
			}
		}
	}
	return m, nil
}

func (m *Manager) Enabled() bool { return m != nil && m.enabled }

func (m *Manager) Authenticate(raw string) (string, bool) {
	if !m.Enabled() {
		return "", true
	}
	raw = normalizeToken(raw)
	m.mu.Lock()
	credential, ok := m.keys[hash(raw)]
	m.mu.Unlock()
	return credential.Tenant, ok
}

// AuthenticateCredential returns the complete scope bound to a credential.
func (m *Manager) AuthenticateCredential(raw string) (Credential, bool) {
	if !m.Enabled() {
		return Credential{}, true
	}
	m.mu.Lock()
	credential, ok := m.keys[hash(normalizeToken(raw))]
	m.mu.Unlock()
	return credential, ok
}

// ClaimNamespace binds a namespace to the first daemon identity that registers
// it. Normal API calls cannot replace this persisted binding.
func (m *Manager) ClaimNamespace(ctx context.Context, namespace, identity string) error {
	if !m.Enabled() {
		return nil
	}
	namespace = strings.ToLower(strings.TrimSpace(namespace))
	identity = strings.TrimSpace(identity)
	if namespace == "" || identity == "" {
		return ErrDaemonIdentity
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if owner := m.owners[namespace]; owner != "" {
		if strings.EqualFold(owner, identity) {
			return nil
		}
		return ErrNamespaceOwned
	}
	m.owners[namespace] = identity
	if err := m.store.Save(ctx, m.stateLocked()); err != nil {
		delete(m.owners, namespace)
		return err
	}
	return nil
}

func (m *Manager) NamespaceOwner(namespace string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.owners[strings.ToLower(strings.TrimSpace(namespace))]
}

// ConfigureNamespace persists visibility and optional owner metadata.
func (m *Manager) ConfigureNamespace(ctx context.Context, namespace string, private bool, ownerIdentity string) error {
	if !m.Enabled() {
		return nil
	}
	namespace = strings.ToLower(strings.TrimSpace(namespace))
	ownerIdentity = strings.TrimSpace(ownerIdentity)
	if namespace == "" {
		return ErrDaemonIdentity
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if owner := m.owners[namespace]; owner != "" && ownerIdentity != "" && !strings.EqualFold(owner, ownerIdentity) {
		return ErrNamespaceOwned
	}
	m.private[namespace] = private
	if ownerIdentity != "" {
		m.owners[namespace] = ownerIdentity
	}
	if err := m.store.Save(ctx, m.stateLocked()); err != nil {
		delete(m.private, namespace)
		return err
	}
	return nil
}

// AuthorizeNamespace enforces tenant isolation and private namespace
// visibility. Private access is limited to the persisted owner daemon or a
// namespace-scoped credential issued by pairing exchange.
func (m *Manager) AuthorizeNamespace(raw, namespace, agentID string) bool {
	if !m.Enabled() {
		return true
	}
	namespace = strings.ToLower(strings.TrimSpace(namespace))
	m.mu.Lock()
	credential, ok := m.keys[hash(normalizeToken(raw))]
	private := m.private[namespace]
	owner := m.owners[namespace]
	m.mu.Unlock()
	if !ok || !strings.EqualFold(strings.TrimSpace(credential.Tenant), namespace) {
		return false
	}
	if !private {
		return true
	}
	if credential.Scope == "namespace" {
		return true
	}
	return credential.Scope == "daemon" &&
		owner != "" &&
		strings.EqualFold(owner, credential.Identity) &&
		strings.EqualFold(owner, strings.TrimSpace(agentID))
}

// RotateDaemonKey replaces a daemon-scoped credential after authenticating the
// currently valid key. The old key is removed in the same persisted state
// update, and the replacement retains the credential's tenant and identity.
func (m *Manager) RotateDaemonKey(ctx context.Context, current, identity string) (string, string, error) {
	if !m.Enabled() {
		return "", "", ErrInvalidCredentials
	}
	current = normalizeToken(current)
	identity = strings.TrimSpace(identity)
	m.mu.Lock()
	defer m.mu.Unlock()
	credential, ok := m.keys[hash(current)]
	if !ok {
		return "", "", ErrInvalidCredentials
	}
	if credential.Scope != "daemon" {
		return "", "", ErrDaemonCredential
	}
	if identity == "" || credential.Identity == "" || credential.Identity != identity {
		return "", "", ErrDaemonIdentity
	}
	if owner := m.owners[strings.ToLower(strings.TrimSpace(credential.Tenant))]; owner != "" &&
		!strings.EqualFold(owner, credential.Identity) {
		return "", "", ErrNamespaceOwned
	}
	replacement, err := randomToken()
	if err != nil {
		return "", "", err
	}
	oldHash := hash(current)
	newHash := hash(replacement)
	namespace := strings.ToLower(strings.TrimSpace(credential.Tenant))
	claimed := false
	if m.owners[namespace] == "" {
		m.owners[namespace] = credential.Identity
		claimed = true
	}
	delete(m.keys, oldHash)
	m.keys[newHash] = Credential{Tenant: credential.Tenant, Scope: credential.Scope, Identity: credential.Identity}
	if err := m.store.Save(ctx, m.stateLocked()); err != nil {
		delete(m.keys, newHash)
		m.keys[oldHash] = credential
		if claimed {
			delete(m.owners, namespace)
		}
		return "", "", err
	}
	return replacement, credential.Tenant, nil
}

func (m *Manager) Exchange(ctx context.Context, code string) (string, string, error) {
	if !m.Enabled() {
		return "", "", ErrInvalidCredentials
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	codeHash := hash(code)
	credential, ok := m.codes[codeHash]
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
	m.keys[hash(key)] = credential
	m.used[codeHash] = true
	state := m.stateLocked()
	if err := m.store.Save(ctx, state); err != nil {
		delete(m.keys, hash(key))
		delete(m.used, codeHash)
		return "", "", err
	}
	return key, credential.Tenant, nil
}

// GeneratePairingCode creates a short-lived, single-use human-readable code.
func (m *Manager) GeneratePairingCode(ctx context.Context, tenant string, ttl time.Duration) (string, error) {
	return m.generatePairingCode(ctx, tenant, "", "", ttl)
}

// GenerateDaemonPairingCode creates a one-time code that issues a daemon key
// bound to one tenant and daemon identity.
func (m *Manager) GenerateDaemonPairingCode(ctx context.Context, tenant, identity string, ttl time.Duration) (string, error) {
	if strings.TrimSpace(identity) == "" {
		return "", ErrDaemonIdentity
	}
	return m.generatePairingCode(ctx, tenant, "daemon", identity, ttl)
}

func (m *Manager) generatePairingCode(ctx context.Context, tenant, scope, identity string, ttl time.Duration) (string, error) {
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
	m.codes[codeHash] = Credential{Tenant: strings.TrimSpace(tenant), Scope: strings.TrimSpace(scope), Identity: strings.TrimSpace(identity)}
	m.expires[codeHash] = time.Now().Add(ttl)
	state := m.stateLocked()
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

func normalizeToken(raw string) string {
	raw = strings.TrimSpace(raw)
	return strings.TrimSpace(strings.TrimPrefix(raw, "Bearer "))
}

func (m *Manager) stateLocked() State {
	state := State{PairingCodes: make(map[string]string), PairingCredentials: make(map[string]Credential), PairingExpiresAt: make(map[string]time.Time), UsedCodes: make(map[string]bool), NamespaceOwners: make(map[string]string), PrivateNamespaces: make(map[string]bool)}
	for hashValue, credential := range m.keys {
		state.Keys = append(state.Keys, Credential{Hash: hashValue, Tenant: credential.Tenant, Scope: credential.Scope, Identity: credential.Identity})
	}
	for hashValue, credential := range m.codes {
		state.PairingCodes[hashValue] = credential.Tenant
		if credential.Scope != "" || credential.Identity != "" {
			state.PairingCredentials[hashValue] = credential
		}
		if expires, ok := m.expires[hashValue]; ok {
			state.PairingExpiresAt[hashValue] = expires
		}
		if m.used[hashValue] {
			state.UsedCodes[hashValue] = true
		}
	}
	for namespace, identity := range m.owners {
		state.NamespaceOwners[namespace] = identity
	}
	for namespace, private := range m.private {
		state.PrivateNamespaces[namespace] = private
	}
	return state
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
	out := State{Keys: append([]Credential(nil), in.Keys...), PairingCodes: make(map[string]string, len(in.PairingCodes)), PairingCredentials: make(map[string]Credential, len(in.PairingCredentials)), PairingExpiresAt: make(map[string]time.Time, len(in.PairingExpiresAt)), UsedCodes: make(map[string]bool, len(in.UsedCodes)), NamespaceOwners: make(map[string]string, len(in.NamespaceOwners)), PrivateNamespaces: make(map[string]bool, len(in.PrivateNamespaces))}
	for key, value := range in.PairingCodes {
		out.PairingCodes[key] = value
	}
	for key, value := range in.UsedCodes {
		out.UsedCodes[key] = value
	}
	for key, value := range in.PairingCredentials {
		out.PairingCredentials[key] = value
	}
	for key, value := range in.PairingExpiresAt {
		out.PairingExpiresAt[key] = value
	}
	for key, value := range in.NamespaceOwners {
		out.NamespaceOwners[key] = value
	}
	for key, value := range in.PrivateNamespaces {
		out.PrivateNamespaces[key] = value
	}
	return out
}
