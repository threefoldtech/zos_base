package geoip

import (
	"net/http"
	"testing"

	"github.com/jarcoal/httpmock"
	"github.com/stretchr/testify/require"
	"github.com/threefoldtech/zos_base/pkg/environment"
)

func TestGetLocation(t *testing.T) {
	httpmock.ActivateNonDefault(defaultHTTPClient.HTTPClient)
	defer httpmock.DeactivateAndReset()

	require := require.New(t)

	t.Run("test valid response", func(t *testing.T) {
		l := Location{
			Continent: "Africa",
			Country:   "Egypt",
			City:      "Cairo",
		}

		env := environment.MustGet()
		for _, url := range env.GeoipURLs {
			httpmock.RegisterResponder("GET", url,
				func(req *http.Request) (*http.Response, error) {
					return httpmock.NewJsonResponse(200, l)
				},
			)

			resp, err := getLocation(url)
			require.NoError(err)
			require.Equal(resp, l)
		}
	})

	l := Location{
		Continent: "Unknown",
		Country:   "Unknown",
		City:      "Unknown",
	}

	t.Run("test 404 status code", func(t *testing.T) {
		env := environment.MustGet()
		for _, url := range env.GeoipURLs {
			httpmock.RegisterResponder("GET", url,
				func(req *http.Request) (*http.Response, error) {
					return httpmock.NewJsonResponse(404, l)
				},
			)

			resp, err := getLocation(url)
			require.Error(err)
			require.Equal(resp, l)
		}
	})

	t.Run("test invalid response data", func(t *testing.T) {
		env := environment.MustGet()
		for _, url := range env.GeoipURLs {
			httpmock.RegisterResponder("GET", url,
				func(req *http.Request) (*http.Response, error) {
					resp, err := httpmock.NewJsonResponse(200, "Cairo")
					return resp, err
				},
			)
			resp, err := getLocation(url)
			require.Error(err)
			require.Equal(resp, l)
		}
	})
}
