package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidatePathZeroRepairUnit(t *testing.T) {
	tests := []struct {
		name        string
		uri         string
		expectedErr error
		expected    string
	}{
		{
			name:        "valid path",
			uri:         "/api/orders",
			expectedErr: nil,
			expected:    "/api/orders",
		},
		{
			name:        "valid path with query string",
			uri:         "/api/orders?status=active",
			expectedErr: nil,
			expected:    "/api/orders",
		},
		{
			name:        "valid path with fragment",
			uri:         "/api/orders#section",
			expectedErr: nil,
			expected:    "/api/orders",
		},
		{
			name:        "double slash",
			uri:         "//api/orders",
			expectedErr: ErrPathContainsDoubleSlash,
		},
		{
			name:        "raw nul byte",
			uri:         "/api/orders\x00/test",
			expectedErr: ErrPathContainsNulByte,
		},
		{
			name:        "encoded nul byte",
			uri:         "/api/orders%00/test",
			expectedErr: ErrPathContainsNulByte,
		},
		{
			name:        "encoded slash lowercase",
			uri:         "/api/orders%2fadmin",
			expectedErr: ErrPathContainsEncodedSlash,
		},
		{
			name:        "encoded slash uppercase",
			uri:         "/api/orders%2Fadmin",
			expectedErr: ErrPathContainsEncodedSlash,
		},
		{
			name:        "encoded backslash",
			uri:         "/api/orders%5cadmin",
			expectedErr: ErrPathContainsEncodedSlash,
		},
		{
			name:        "literal backslash",
			uri:         "/api/orders\\admin",
			expectedErr: ErrPathContainsEncodedSlash,
		},
		{
			name:        "dot dot traversal",
			uri:         "/api/orders/../admin",
			expectedErr: ErrPathContainsTraversal,
		},
		{
			name:        "dot dot suffix",
			uri:         "/api/orders/..",
			expectedErr: ErrPathContainsTraversal,
		},
		{
			name:        "exact dot dot",
			uri:         "..",
			expectedErr: ErrPathContainsTraversal,
		},
		{
			name:        "single dot segment",
			uri:         "/api/orders/./test",
			expectedErr: ErrPathContainsTraversal,
		},
		{
			name:        "single dot suffix",
			uri:         "/api/orders/.",
			expectedErr: ErrPathContainsTraversal,
		},
		{
			name:        "percent encoded dot dot",
			uri:         "/api/orders/%2e%2e/admin",
			expectedErr: ErrPathContainsTraversal,
		},
		{
			name:        "mixed percent encoded dot dot",
			uri:         "/api/orders/.%2e/admin",
			expectedErr: ErrPathContainsTraversal,
		},
		{
			name:        "no leading slash",
			uri:         "api/orders",
			expectedErr: ErrPathInvalidPrefix,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://example.local", nil)
			req.RequestURI = tt.uri

			validated, err := ValidatePathZeroRepair(req)
			if tt.expectedErr != nil {
				require.ErrorIs(t, err, tt.expectedErr)
				assert.Empty(t, validated)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expected, validated)
			}
		})
	}
}
