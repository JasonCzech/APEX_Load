package api

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"

	"github.com/jasonczech/apexload/pkg/keywrap"
)

// A table key is wrapped once. Every journal/checkpoint/seed envelope gets a
// fresh GCM nonce and a distinct authenticated purpose/revision. Row values do
// not enter unencrypted lifecycle metadata or ordinary archive provenance.
type liveTableCipher struct {
	aead    cipher.AEAD
	wrapped *keywrap.WrappedKey
}
type liveTableEnvelope struct {
	Version    int    `json:"version"`
	Ciphertext []byte `json:"ciphertext"`
}

func newLiveTableCipher(ctx context.Context, wrapper keywrap.KeyWrapper, id string) (*liveTableCipher, error) {
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, err
	}
	defer clearRunSnapshotBytes(key)
	c, err := liveCipherForKey(key)
	if err != nil {
		return nil, err
	}
	if wrapper != nil {
		wrapped, err := wrapper.WrapKey(ctx, key, []byte("apexload/live-table-key/v1/"+id))
		if err != nil {
			return nil, err
		}
		c.wrapped = &wrapped
	}
	return c, nil
}
func restoreLiveTableCipher(ctx context.Context, wrapper keywrap.KeyWrapper, id string, wrapped *keywrap.WrappedKey) (*liveTableCipher, error) {
	if wrapper == nil || wrapped == nil {
		return nil, errors.New("live table recovery key is unavailable")
	}
	key, err := wrapper.UnwrapKey(ctx, *wrapped, []byte("apexload/live-table-key/v1/"+id))
	if err != nil {
		return nil, err
	}
	defer clearRunSnapshotBytes(key)
	c, err := liveCipherForKey(key)
	if err == nil {
		c.wrapped = wrapped
	}
	return c, err
}
func liveCipherForKey(key []byte) (*liveTableCipher, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &liveTableCipher{aead: aead}, nil
}
func (c *liveTableCipher) seal(value any, aad string) ([]byte, error) {
	plaintext, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	defer clearRunSnapshotBytes(plaintext)
	if len(plaintext) > 24<<20 {
		return nil, errors.New("live table encrypted payload exceeds limit")
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return json.Marshal(liveTableEnvelope{Version: 1, Ciphertext: c.aead.Seal(nonce, nonce, plaintext, []byte(aad))})
}
func (c *liveTableCipher) open(body []byte, aad string, out any) error {
	if len(body) > 32<<20 {
		return errors.New("live table encrypted payload exceeds limit")
	}
	var envelope liveTableEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil || envelope.Version != 1 || len(envelope.Ciphertext) < c.aead.NonceSize() {
		return errors.New("invalid live table envelope")
	}
	plaintext, err := c.aead.Open(nil, envelope.Ciphertext[:c.aead.NonceSize()], envelope.Ciphertext[c.aead.NonceSize():], []byte(aad))
	if err != nil {
		return errors.New("live table envelope authentication failed")
	}
	defer clearRunSnapshotBytes(plaintext)
	return json.Unmarshal(plaintext, out)
}
