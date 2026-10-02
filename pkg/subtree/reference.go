// Package subtree defines the versioned CAS directory reference shared by the
// router and root fetcher. The helper transports it without interpreting it.
package subtree

import (
	"encoding/base64"
	"fmt"
	"strings"

	remoteexecution "github.com/bazelbuild/remote-apis/build/bazel/remote/execution/v2"
	"github.com/buildbarn/bb-storage/pkg/digest"
)

// Prefix separates dependency tree references from Docker image references.
const Prefix = "cas-v1:"

// Reference includes the instance, digest function, hash and size. Encoding
// avoids delimiters and newlines in the fetcher's line-oriented protocol.
func Reference(d digest.Digest) string {
	return Prefix + base64.RawURLEncoding.EncodeToString([]byte(d.GetByteStreamReadPath(remoteexecution.Compressor_IDENTITY)))
}

// ParseReference accepts only canonical, uncompressed directory references.
func ParseReference(ref string) (digest.Digest, error) {
	if !strings.HasPrefix(ref, Prefix) || len(ref) > 8192 {
		return digest.Digest{}, fmt.Errorf("invalid dependency reference")
	}
	data, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(ref, Prefix))
	if err != nil {
		return digest.Digest{}, err
	}
	d, compressor, err := digest.NewDigestFromByteStreamReadPath(string(data))
	if err != nil {
		return digest.Digest{}, err
	}
	if compressor != remoteexecution.Compressor_IDENTITY || Reference(d) != ref {
		return digest.Digest{}, fmt.Errorf("noncanonical dependency reference")
	}
	return d, nil
}
