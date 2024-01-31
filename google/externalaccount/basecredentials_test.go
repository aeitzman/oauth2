// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package externalaccount

import (
	"context"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

const (
	textBaseCredPath             = "testdata/3pi_cred.txt"
	jsonBaseCredPath             = "testdata/3pi_cred.json"
	baseImpersonateCredsReqBody  = "audience=32555940559.apps.googleusercontent.com&grant_type=urn%3Aietf%3Aparams%3Aoauth%3Agrant-type%3Atoken-exchange&requested_token_type=urn%3Aietf%3Aparams%3Aoauth%3Atoken-type%3Aaccess_token&scope=https%3A%2F%2Fwww.googleapis.com%2Fauth%2Fcloud-platform&subject_token=street123&subject_token_type=urn%3Aietf%3Aparams%3Aoauth%3Atoken-type%3Ajwt"
	baseImpersonateCredsRespBody = `{"accessToken":"Second.Access.Token","expireTime":"2020-12-28T15:01:23Z"}`
)

var testBaseCredSource = CredentialSource{
	File:   textBaseCredPath,
	Format: Format{Type: fileTypeText},
}

var testConfig = Config{
	Audience:         "32555940559.apps.googleusercontent.com",
	SubjectTokenType: "urn:ietf:params:oauth:token-type:jwt",
	TokenInfoURL:     "http://localhost:8080/v1/tokeninfo",
	ClientSecret:     "notsosecret",
	ClientID:         "rbrgnognrhongo3bi4gb9ghg9g",
	CredentialSource: &testBaseCredSource,
	Scopes:           []string{"https://www.googleapis.com/auth/devstorage.full_control"},
}

var (
	baseCredsRequestBody                          = "audience=32555940559.apps.googleusercontent.com&grant_type=urn%3Aietf%3Aparams%3Aoauth%3Agrant-type%3Atoken-exchange&requested_token_type=urn%3Aietf%3Aparams%3Aoauth%3Atoken-type%3Aaccess_token&scope=https%3A%2F%2Fwww.googleapis.com%2Fauth%2Fdevstorage.full_control&subject_token=street123&subject_token_type=urn%3Aietf%3Aparams%3Aoauth%3Atoken-type%3Aid_token"
	baseCredsResponseBody                         = `{"access_token":"Sample.Access.Token","issued_token_type":"urn:ietf:params:oauth:token-type:access_token","token_type":"Bearer","expires_in":3600,"scope":"https://www.googleapis.com/auth/cloud-platform"}`
	workforcePoolRequestBodyWithClientId          = "audience=%2F%2Fiam.googleapis.com%2Flocations%2Feu%2FworkforcePools%2Fpool-id%2Fproviders%2Fprovider-id&grant_type=urn%3Aietf%3Aparams%3Aoauth%3Agrant-type%3Atoken-exchange&requested_token_type=urn%3Aietf%3Aparams%3Aoauth%3Atoken-type%3Aaccess_token&scope=https%3A%2F%2Fwww.googleapis.com%2Fauth%2Fdevstorage.full_control&subject_token=street123&subject_token_type=urn%3Aietf%3Aparams%3Aoauth%3Atoken-type%3Aid_token"
	workforcePoolRequestBodyWithoutClientId       = "audience=%2F%2Fiam.googleapis.com%2Flocations%2Feu%2FworkforcePools%2Fpool-id%2Fproviders%2Fprovider-id&grant_type=urn%3Aietf%3Aparams%3Aoauth%3Agrant-type%3Atoken-exchange&options=%7B%22userProject%22%3A%22myProject%22%7D&requested_token_type=urn%3Aietf%3Aparams%3Aoauth%3Atoken-type%3Aaccess_token&scope=https%3A%2F%2Fwww.googleapis.com%2Fauth%2Fdevstorage.full_control&subject_token=street123&subject_token_type=urn%3Aietf%3Aparams%3Aoauth%3Atoken-type%3Aid_token"
	correctAT                                     = "Sample.Access.Token"
	expiry                                  int64 = 234852
)
var (
	testNow = func() time.Time { return time.Unix(expiry, 0) }
)

type testExchangeTokenServer struct {
	url           string
	authorization string
	contentType   string
	metricsHeader string
	body          string
	response      string
}

func run(t *testing.T, config *Config, tets *testExchangeTokenServer) (*oauth2.Token, error) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.String(), tets.url; got != want {
			t.Errorf("URL.String(): got %v but want %v", got, want)
		}
		headerAuth := r.Header.Get("Authorization")
		if got, want := headerAuth, tets.authorization; got != want {
			t.Errorf("got %v but want %v", got, want)
		}
		headerContentType := r.Header.Get("Content-Type")
		if got, want := headerContentType, tets.contentType; got != want {
			t.Errorf("got %v but want %v", got, want)
		}
		headerMetrics := r.Header.Get("x-goog-api-client")
		if got, want := headerMetrics, tets.metricsHeader; got != want {
			t.Errorf("got %v but want %v", got, want)
		}
		body, err := ioutil.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("Failed reading request body: %s.", err)
		}
		if got, want := string(body), tets.body; got != want {
			t.Errorf("Unexpected exchange payload: got %v but want %v", got, want)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(tets.response))
	}))
	defer server.Close()
	config.TokenURL = server.URL

	oldNow := now
	defer func() { now = oldNow }()
	now = testNow

	ts := tokenSource{
		ctx:  context.Background(),
		conf: config,
	}

	return ts.Token()
}

func validateToken(t *testing.T, tok *oauth2.Token) {
	if got, want := tok.AccessToken, correctAT; got != want {
		t.Errorf("Unexpected access token: got %v, but wanted %v", got, want)
	}
	if got, want := tok.TokenType, "Bearer"; got != want {
		t.Errorf("Unexpected TokenType: got %v, but wanted %v", got, want)
	}

	if got, want := tok.Expiry, testNow().Add(time.Duration(3600)*time.Second); got != want {
		t.Errorf("Unexpected Expiry: got %v, but wanted %v", got, want)
	}
}

func createImpersonationServer(urlWanted, authWanted, bodyWanted, response string, t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.String(), urlWanted; got != want {
			t.Errorf("URL.String(): got %v but want %v", got, want)
		}
		headerAuth := r.Header.Get("Authorization")
		if got, want := headerAuth, authWanted; got != want {
			t.Errorf("got %v but want %v", got, want)
		}
		headerContentType := r.Header.Get("Content-Type")
		if got, want := headerContentType, "application/json"; got != want {
			t.Errorf("got %v but want %v", got, want)
		}
		body, err := ioutil.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("Failed reading request body: %v.", err)
		}
		if got, want := string(body), bodyWanted; got != want {
			t.Errorf("Unexpected impersonation payload: got %v but want %v", got, want)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(response))
	}))
}

func createTargetServer(metricsHeaderWanted string, t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.String(), "/"; got != want {
			t.Errorf("URL.String(): got %v but want %v", got, want)
		}
		headerAuth := r.Header.Get("Authorization")
		if got, want := headerAuth, "Basic cmJyZ25vZ25yaG9uZ28zYmk0Z2I5Z2hnOWc6bm90c29zZWNyZXQ="; got != want {
			t.Errorf("got %v but want %v", got, want)
		}
		headerContentType := r.Header.Get("Content-Type")
		if got, want := headerContentType, "application/x-www-form-urlencoded"; got != want {
			t.Errorf("got %v but want %v", got, want)
		}
		headerMetrics := r.Header.Get("x-goog-api-client")
		if got, want := headerMetrics, metricsHeaderWanted; got != want {
			t.Errorf("got %v but want %v", got, want)
		}
		body, err := ioutil.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("Failed reading request body: %v.", err)
		}
		if got, want := string(body), baseImpersonateCredsReqBody; got != want {
			t.Errorf("Unexpected exchange payload: got %v but want %v", got, want)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(baseCredsResponseBody))
	}))
}

func getExpectedMetricsHeader(source string, saImpersonation bool, configLifetime bool) string {
	return fmt.Sprintf("gl-go/%s auth/unknown google-byoid-sdk source/%s sa-impersonation/%t config-lifetime/%t", goVersion(), source, saImpersonation, configLifetime)
}

func TestToken(t *testing.T) {
	config := Config{
		Audience:         "32555940559.apps.googleusercontent.com",
		SubjectTokenType: "urn:ietf:params:oauth:token-type:id_token",
		ClientSecret:     "notsosecret",
		ClientID:         "rbrgnognrhongo3bi4gb9ghg9g",
		CredentialSource: &testBaseCredSource,
		Scopes:           []string{"https://www.googleapis.com/auth/devstorage.full_control"},
	}

	server := testExchangeTokenServer{
		url:           "/",
		authorization: "Basic cmJyZ25vZ25yaG9uZ28zYmk0Z2I5Z2hnOWc6bm90c29zZWNyZXQ=",
		contentType:   "application/x-www-form-urlencoded",
		metricsHeader: getExpectedMetricsHeader("file", false, false),
		body:          baseCredsRequestBody,
		response:      baseCredsResponseBody,
	}

	tok, err := run(t, &config, &server)

	if err != nil {
		t.Fatalf("Unexpected error: %e", err)
	}
	validateToken(t, tok)
}

func TestWorkforcePoolTokenWithClientID(t *testing.T) {
	config := Config{
		Audience:                 "//iam.googleapis.com/locations/eu/workforcePools/pool-id/providers/provider-id",
		SubjectTokenType:         "urn:ietf:params:oauth:token-type:id_token",
		ClientSecret:             "notsosecret",
		ClientID:                 "rbrgnognrhongo3bi4gb9ghg9g",
		CredentialSource:         &testBaseCredSource,
		Scopes:                   []string{"https://www.googleapis.com/auth/devstorage.full_control"},
		WorkforcePoolUserProject: "myProject",
	}

	server := testExchangeTokenServer{
		url:           "/",
		authorization: "Basic cmJyZ25vZ25yaG9uZ28zYmk0Z2I5Z2hnOWc6bm90c29zZWNyZXQ=",
		contentType:   "application/x-www-form-urlencoded",
		metricsHeader: getExpectedMetricsHeader("file", false, false),
		body:          workforcePoolRequestBodyWithClientId,
		response:      baseCredsResponseBody,
	}

	tok, err := run(t, &config, &server)

	if err != nil {
		t.Fatalf("Unexpected error: %e", err)
	}
	validateToken(t, tok)
}

func TestWorkforcePoolTokenWithoutClientID(t *testing.T) {
	config := Config{
		Audience:                 "//iam.googleapis.com/locations/eu/workforcePools/pool-id/providers/provider-id",
		SubjectTokenType:         "urn:ietf:params:oauth:token-type:id_token",
		ClientSecret:             "notsosecret",
		CredentialSource:         &testBaseCredSource,
		Scopes:                   []string{"https://www.googleapis.com/auth/devstorage.full_control"},
		WorkforcePoolUserProject: "myProject",
	}

	server := testExchangeTokenServer{
		url:           "/",
		authorization: "",
		contentType:   "application/x-www-form-urlencoded",
		metricsHeader: getExpectedMetricsHeader("file", false, false),
		body:          workforcePoolRequestBodyWithoutClientId,
		response:      baseCredsResponseBody,
	}

	tok, err := run(t, &config, &server)

	if err != nil {
		t.Fatalf("Unexpected error: %e", err)
	}
	validateToken(t, tok)
}

func TestNonworkforceWithWorkforcePoolUserProject(t *testing.T) {
	config := Config{
		Audience:                 "32555940559.apps.googleusercontent.com",
		SubjectTokenType:         "urn:ietf:params:oauth:token-type:id_token",
		TokenURL:                 "https://sts.googleapis.com",
		ClientSecret:             "notsosecret",
		ClientID:                 "rbrgnognrhongo3bi4gb9ghg9g",
		CredentialSource:         &testBaseCredSource,
		Scopes:                   []string{"https://www.googleapis.com/auth/devstorage.full_control"},
		WorkforcePoolUserProject: "myProject",
	}

	_, err := NewTokenSource(context.Background(), config)

	if err == nil {
		t.Fatalf("Expected error but found none")
	}
	if got, want := err.Error(), "oauth2/google: Workforce pool user project should not be set for non-workforce pool credentials"; got != want {
		t.Errorf("Incorrect error received.\nExpected: %s\nRecieved: %s", want, got)
	}
}

func TestWorkforcePoolCreation(t *testing.T) {
	var audienceValidatyTests = []struct {
		audience      string
		expectSuccess bool
	}{
		{"//iam.googleapis.com/locations/global/workforcePools/pool-id/providers/provider-id", true},
		{"//iam.googleapis.com/locations/eu/workforcePools/pool-id/providers/provider-id", true},
		{"//iam.googleapis.com/locations/eu/workforcePools/workloadIdentityPools/providers/provider-id", true},
		{"identitynamespace:1f12345:my_provider", false},
		{"//iam.googleapis.com/projects/123456/locations/global/workloadIdentityPools/pool-id/providers/provider-id", false},
		{"//iam.googleapis.com/projects/123456/locations/eu/workloadIdentityPools/pool-id/providers/provider-id", false},
		{"//iam.googleapis.com/projects/123456/locations/global/workloadIdentityPools/workforcePools/providers/provider-id", false},
		{"//iamgoogleapis.com/locations/eu/workforcePools/pool-id/providers/provider-id", false},
		{"//iam.googleapiscom/locations/eu/workforcePools/pool-id/providers/provider-id", false},
		{"//iam.googleapis.com/locations/workforcePools/pool-id/providers/provider-id", false},
		{"//iam.googleapis.com/locations/eu/workforcePool/pool-id/providers/provider-id", false},
		{"//iam.googleapis.com/locations//workforcePool/pool-id/providers/provider-id", false},
	}

	ctx := context.Background()
	for _, tt := range audienceValidatyTests {
		t.Run(" "+tt.audience, func(t *testing.T) { // We prepend a space ahead of the test input when outputting for sake of readability.
			config := testConfig
			config.TokenURL = "https://sts.googleapis.com" // Setting the most basic acceptable tokenURL
			config.ServiceAccountImpersonationURL = "https://iamcredentials.googleapis.com"
			config.Audience = tt.audience
			config.WorkforcePoolUserProject = "myProject"
			_, err := NewTokenSource(ctx, config)

			if tt.expectSuccess && err != nil {
				t.Errorf("got %v but want nil", err)
			} else if !tt.expectSuccess && err == nil {
				t.Errorf("got nil but expected an error")
			}
		})
	}
}

var impersonationTests = []struct {
	name                      string
	config                    Config
	expectedImpersonationBody string
	expectedMetricsHeader     string
}{
	{
		name: "Base Impersonation",
		config: Config{
			Audience:         "32555940559.apps.googleusercontent.com",
			SubjectTokenType: "urn:ietf:params:oauth:token-type:jwt",
			TokenInfoURL:     "http://localhost:8080/v1/tokeninfo",
			ClientSecret:     "notsosecret",
			ClientID:         "rbrgnognrhongo3bi4gb9ghg9g",
			CredentialSource: &testBaseCredSource,
			Scopes:           []string{"https://www.googleapis.com/auth/devstorage.full_control"},
		},
		expectedImpersonationBody: "{\"lifetime\":\"3600s\",\"scope\":[\"https://www.googleapis.com/auth/devstorage.full_control\"]}",
		expectedMetricsHeader:     getExpectedMetricsHeader("file", true, false),
	},
	{
		name: "With TokenLifetime Set",
		config: Config{
			Audience:         "32555940559.apps.googleusercontent.com",
			SubjectTokenType: "urn:ietf:params:oauth:token-type:jwt",
			TokenInfoURL:     "http://localhost:8080/v1/tokeninfo",
			ClientSecret:     "notsosecret",
			ClientID:         "rbrgnognrhongo3bi4gb9ghg9g",
			CredentialSource: &testBaseCredSource,
			Scopes:           []string{"https://www.googleapis.com/auth/devstorage.full_control"},
			ServiceAccountImpersonationLifetimeSeconds: 10000,
		},
		expectedImpersonationBody: "{\"lifetime\":\"10000s\",\"scope\":[\"https://www.googleapis.com/auth/devstorage.full_control\"]}",
		expectedMetricsHeader:     getExpectedMetricsHeader("file", true, true),
	},
}

func TestImpersonation(t *testing.T) {
	for _, tt := range impersonationTests {
		t.Run(tt.name, func(t *testing.T) {
			testImpersonateConfig := tt.config
			impersonateServer := createImpersonationServer("/", "Bearer Sample.Access.Token", tt.expectedImpersonationBody, baseImpersonateCredsRespBody, t)
			defer impersonateServer.Close()
			testImpersonateConfig.ServiceAccountImpersonationURL = impersonateServer.URL

			targetServer := createTargetServer(tt.expectedMetricsHeader, t)
			defer targetServer.Close()
			testImpersonateConfig.TokenURL = targetServer.URL

			ourTS, err := testImpersonateConfig.tokenSource(context.Background(), "http")
			if err != nil {
				t.Fatalf("Failed to create TokenSource: %v", err)
			}

			oldNow := now
			defer func() { now = oldNow }()
			now = testNow

			tok, err := ourTS.Token()
			if err != nil {
				t.Fatalf("Unexpected error: %e", err)
			}
			if got, want := tok.AccessToken, "Second.Access.Token"; got != want {
				t.Errorf("Unexpected access token: got %v, but wanted %v", got, want)
			}
			if got, want := tok.TokenType, "Bearer"; got != want {
				t.Errorf("Unexpected TokenType: got %v, but wanted %v", got, want)
			}
		})
	}
}

var newTokenTests = []struct {
	name   string
	config Config
}{
	{
		name: "Missing Audience",
		config: Config{
			SubjectTokenType: "urn:ietf:params:oauth:token-type:jwt",
			TokenInfoURL:     "http://localhost:8080/v1/tokeninfo",
			ClientSecret:     "notsosecret",
			ClientID:         "rbrgnognrhongo3bi4gb9ghg9g",
			CredentialSource: &testBaseCredSource,
			Scopes:           []string{"https://www.googleapis.com/auth/devstorage.full_control"},
			ServiceAccountImpersonationLifetimeSeconds: 10000,
		},
	},
	{
		name: "Missing Subject Token Type",
		config: Config{
			Audience:         "32555940559.apps.googleusercontent.com",
			TokenInfoURL:     "http://localhost:8080/v1/tokeninfo",
			ClientSecret:     "notsosecret",
			ClientID:         "rbrgnognrhongo3bi4gb9ghg9g",
			CredentialSource: &testBaseCredSource,
			Scopes:           []string{"https://www.googleapis.com/auth/devstorage.full_control"},
			ServiceAccountImpersonationLifetimeSeconds: 10000,
		},
	},
	{
		name: "No Cred Source",
		config: Config{
			Audience:         "32555940559.apps.googleusercontent.com",
			SubjectTokenType: "urn:ietf:params:oauth:token-type:jwt",
			TokenInfoURL:     "http://localhost:8080/v1/tokeninfo",
			ClientSecret:     "notsosecret",
			ClientID:         "rbrgnognrhongo3bi4gb9ghg9g",
			Scopes:           []string{"https://www.googleapis.com/auth/devstorage.full_control"},
			ServiceAccountImpersonationLifetimeSeconds: 10000,
		},
	},
	{
		name: "Cred Source and Supplier",
		config: Config{
			Audience:                       "32555940559.apps.googleusercontent.com",
			SubjectTokenType:               "urn:ietf:params:oauth:token-type:jwt",
			TokenInfoURL:                   "http://localhost:8080/v1/tokeninfo",
			CredentialSource:               &testBaseCredSource,
			AwsSecurityCredentialsSupplier: testAwsSupplier{},
			ClientSecret:                   "notsosecret",
			ClientID:                       "rbrgnognrhongo3bi4gb9ghg9g",
			Scopes:                         []string{"https://www.googleapis.com/auth/devstorage.full_control"},
			ServiceAccountImpersonationLifetimeSeconds: 10000,
		},
	},
}

func TestNewToken(t *testing.T) {
	for _, tt := range newTokenTests {
		t.Run(tt.name, func(t *testing.T) {
			testConfig := tt.config

			_, err := NewTokenSource(context.Background(), testConfig)
			if err == nil {
				t.Fatalf("expected error when calling NewToken()")
			}
		})
	}
}
