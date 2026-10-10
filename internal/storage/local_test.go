package storage

import (
	"context"
	"io"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLocalRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	local, err := NewLocal(dir, "/v1/media/photos")
	require.NoError(t, err)

	const key = "0123456789abcdef0123456789abcdef.jpg"
	body := []byte{0xff, 0xd8, 0xff, 0xd9}
	require.NoError(t, local.Put(context.Background(), key, "image/jpeg", body))
	require.Equal(t, "/v1/media/photos/"+key, local.PublicPath(key))

	gotKey, err := local.Key(local.PublicPath(key))
	require.NoError(t, err)
	require.Equal(t, key, gotKey)

	f, err := local.Open(key)
	require.NoError(t, err)
	got, err := io.ReadAll(f)
	require.NoError(t, err)
	require.NoError(t, f.Close())
	require.Equal(t, body, got)

	_, err = local.Open("../" + key)
	require.ErrorIs(t, err, ErrInvalidKey)
	require.ErrorIs(t, local.Put(context.Background(), "../secret.jpg", "image/jpeg", body), ErrInvalidKey)
	_, err = local.Key("/v1/media/photos/../../etc/passwd")
	require.ErrorIs(t, err, ErrInvalidKey)

	require.NoError(t, local.Delete(context.Background(), key))
	_, err = local.Open(key)
	require.ErrorIs(t, err, os.ErrNotExist)
	require.NoError(t, local.Delete(context.Background(), key))
}
