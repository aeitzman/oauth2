// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package externalaccount

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google/internal/stsexchange"
)

// now aliases time.Now for testing
var now = func() time.Time {
	return time.Now().UTC()
}

// ExternalAccountConfig is a config that stores the configuration for fetching tokens with external credentials.
type ExternalAccountConfig struct {
	// Audience is the Secure Token Service (STS) audience which contains the resource name for the workload
	// identity pool or the workforce pool and the provider identifier in that pool. Required.
	Audience string
	// SubjectTokenType is the STS token type based on the Oauth2.0 token exchange spec
	// e.g. `urn:ietf:params:oauth:token-type:jwt`. Required.
	SubjectTokenType string
	// TokenURL is the STS token exchange endpoint. Optional, if not provided, will default to
	// https://sts.googleapis.com/v1/token
	TokenURL string
	// TokenInfoURL is the token_info endpoint used to retrieve the account related information (
	// user attributes like account identifier, eg. email, username, uid, etc). This is
	// needed for gCloud session account identification.
	TokenInfoURL string
	// ServiceAccountImpersonationURL is the URL for the service account impersonation request. This is only
	// required for workload identity pools when APIs to be accessed have not integrated with UberMint.
	ServiceAccountImpersonationURL string
	// ServiceAccountImpersonationLifetimeSeconds is the number of seconds the service account impersonation
	// token will be valid for. Optional, if not provided will default to 3600.
	ServiceAccountImpersonationLifetimeSeconds int
	// ClientSecret is currently only required if token_info endpoint also
	// needs to be called with the generated GCP access token. When provided, STS will be
	// called with additional basic authentication using client_id as username and client_secret as password.
	ClientSecret string
	// ClientID is only required in conjunction with ClientSecret, as described above.
	ClientID string
	// CredentialSource contains the necessary information to retrieve the token itself, as well
	// as some environmental information.
	CredentialSource CredentialSource
	// QuotaProjectID is injected by gCloud. If the value is non-empty, the Auth libraries
	// will set the x-goog-user-project which overrides the project associated with the credentials.
	QuotaProjectID string
	// Scopes contains the desired scopes for the returned access token.
	Scopes []string
	// WorkforcePoolUserProject is the optional workforce pool user project number when the credential
	// corresponds to a workforce pool and not a workload identity pool.
	// The underlying principal must still have serviceusage.services.use IAM
	// permission to use the project for billing/quota.
	WorkforcePoolUserProject string
	// SubjectTokenSupplier is an optional token supplier for OIDC/SAML credentials. This should be a function that returns
	// a valid subject token as a string.
	SubjectTokenSupplier func() (string, error) `json:"-"` // Ignore for json.
	// AwsSecurityCredentialsSupplier is an optional AWS Security Credential supplier. This should contain a
	// function that returns valid AwsSecurityCredentials and a valid AwsRegion.
	AwsSecurityCredentialsSupplier *AwsSecurityCredentialsSupplier `json:"-"` // Ignore for json.
}

var (
	validWorkforceAudiencePattern *regexp.Regexp = regexp.MustCompile(`//iam\.googleapis\.com/locations/[^/]+/workforcePools/`)
)

func validateWorkforceAudience(input string) bool {
	return validWorkforceAudiencePattern.MatchString(input)
}

// TokenSource Returns an external account TokenSource struct. This is to be called by package google to construct a google.Credentials.
func (c *ExternalAccountConfig) TokenSource(ctx context.Context) (oauth2.TokenSource, error) {
	return c.tokenSource(ctx, "https")
}

// tokenSource is a private function that's directly called by some of the tests,
// because the unit test URLs are mocked, and would otherwise fail the
// validity check.
func (c *ExternalAccountConfig) tokenSource(ctx context.Context, scheme string) (oauth2.TokenSource, error) {
	if c.WorkforcePoolUserProject != "" {
		valid := validateWorkforceAudience(c.Audience)
		if !valid {
			return nil, fmt.Errorf("oauth2/google: workforce_pool_user_project should not be set for non-workforce pool credentials")
		}
	}

	ts := tokenSource{
		ctx:  ctx,
		conf: c,
	}
	if c.ServiceAccountImpersonationURL == "" {
		return oauth2.ReuseTokenSource(nil, ts), nil
	}
	scopes := c.Scopes
	ts.conf.Scopes = []string{"https://www.googleapis.com/auth/cloud-platform"}
	imp := ImpersonateTokenSource{
		Ctx:                  ctx,
		URL:                  c.ServiceAccountImpersonationURL,
		Scopes:               scopes,
		Ts:                   oauth2.ReuseTokenSource(nil, ts),
		TokenLifetimeSeconds: c.ServiceAccountImpersonationLifetimeSeconds,
	}
	return oauth2.ReuseTokenSource(nil, imp), nil
}

// Subject token file types.
const (
	fileTypeText    = "text"
	fileTypeJSON    = "json"
	defaultTokenUrl = "https://sts.googleapis.com/v1/token"
)

type format struct {
	// Type is either "text" or "json". When not provided "text" type is assumed.
	Type string `json:"type"`
	// SubjectTokenFieldName is only required for JSON format. This would be "access_token" for azure.
	SubjectTokenFieldName string `json:"subject_token_field_name"`
}

// CredentialSource stores the information necessary to retrieve the credentials for the STS exchange.
// One field amongst File, URL, Executable should be filled, depending on the kind of credential in question.
// The EnvironmentID should start with AWS if being used for an AWS credential.
type CredentialSource struct {
	// File is the location for file sourced credentials.
	File string `json:"file"`

	// Url is the URL to call for URL sourced credentials.
	URL string `json:"url"`
	// Headers are the Headers to attach to the request for URL sourced credentials.
	Headers map[string]string `json:"headers"`

	// Executable is the configuration object for executable sourced credentials.
	Executable *ExecutableConfig `json:"executable"`

	// EnvironmentID is the EnvironmentID used for AWS sourced credentials.
	EnvironmentID string `json:"environment_id"`
	// RegionURL is the metadata URL to retrieve the region from for EC2 AWS credentials.
	RegionURL string `json:"region_url"`
	// RegionalCredVerificationURL is the AWS regional credential verification URL, will default to
	//  "https://sts.{region}.amazonaws.com?Action=GetCallerIdentity&Version=2011-06-15" if not provided."
	RegionalCredVerificationURL string `json:"regional_cred_verification_url"`
	// IMDSv2SessionTokenURL is the URL to retrieve the session token when using IMDSv2 in AWS.
	IMDSv2SessionTokenURL string `json:"imdsv2_session_token_url"`
	// Format is the format type for the subject token. Used for File and URL sourced credentials. Expected values are "text" or "json".
	Format format `json:"format"`
}

type ExecutableConfig struct {
	Command       string `json:"command"`
	TimeoutMillis *int   `json:"timeout_millis"`
	OutputFile    string `json:"output_file"`
}

type AwsSecurityCredentialsSupplier struct {
	// AwsRegion is the AWS region.
	AwsRegion string
	// GetAwsSecurityCredentials is a function that should return valid AwsSecurityCredentials.
	GetAwsSecurityCredentials func() (AwsSecurityCredentials, error)
}

// parse determines the type of CredentialSource needed.
func (c *ExternalAccountConfig) parse(ctx context.Context) (baseCredentialSource, error) {
	//set Defaults
	if c.TokenURL == "" {
		c.TokenURL = defaultTokenUrl
	}

	if c.AwsSecurityCredentialsSupplier != nil {
		awsCredSource := awsCredentialSource{
			RegionalCredVerificationURL:    c.CredentialSource.RegionalCredVerificationURL,
			awsSecurityCredentialsSupplier: c.AwsSecurityCredentialsSupplier,
			TargetResource:                 c.Audience,
		}
		return awsCredSource, nil
	} else if c.SubjectTokenSupplier != nil {
		return programmaticRefreshCredentialSource{SubjectTokenSupplier: c.SubjectTokenSupplier}, nil
	} else if len(c.CredentialSource.EnvironmentID) > 3 && c.CredentialSource.EnvironmentID[:3] == "aws" {
		if awsVersion, err := strconv.Atoi(c.CredentialSource.EnvironmentID[3:]); err == nil {
			if awsVersion != 1 {
				return nil, fmt.Errorf("oauth2/google: aws version '%d' is not supported in the current build", awsVersion)
			}

			awsCredSource := awsCredentialSource{
				EnvironmentID:               c.CredentialSource.EnvironmentID,
				RegionURL:                   c.CredentialSource.RegionURL,
				RegionalCredVerificationURL: c.CredentialSource.RegionalCredVerificationURL,
				CredVerificationURL:         c.CredentialSource.URL,
				TargetResource:              c.Audience,
				ctx:                         ctx,
			}
			if c.CredentialSource.IMDSv2SessionTokenURL != "" {
				awsCredSource.IMDSv2SessionTokenURL = c.CredentialSource.IMDSv2SessionTokenURL
			}

			return awsCredSource, nil
		}
	} else if c.CredentialSource.File != "" {
		return fileCredentialSource{File: c.CredentialSource.File, Format: c.CredentialSource.Format}, nil
	} else if c.CredentialSource.URL != "" {
		return urlCredentialSource{URL: c.CredentialSource.URL, Headers: c.CredentialSource.Headers, Format: c.CredentialSource.Format, ctx: ctx}, nil
	} else if c.CredentialSource.Executable != nil {
		return createExecutableCredential(ctx, c.CredentialSource.Executable, c)
	}
	return nil, fmt.Errorf("oauth2/google: unable to parse credential source")
}

type baseCredentialSource interface {
	credentialSourceType() string
	subjectToken() (string, error)
}

// tokenSource is the source that handles external credentials. It is used to retrieve Tokens.
type tokenSource struct {
	ctx  context.Context
	conf *ExternalAccountConfig
}

func getMetricsHeaderValue(conf *ExternalAccountConfig, credSource baseCredentialSource) string {
	return fmt.Sprintf("gl-go/%s auth/%s google-byoid-sdk source/%s sa-impersonation/%t config-lifetime/%t",
		goVersion(),
		"unknown",
		credSource.credentialSourceType(),
		conf.ServiceAccountImpersonationURL != "",
		conf.ServiceAccountImpersonationLifetimeSeconds != 0)
}

// Token allows tokenSource to conform to the oauth2.TokenSource interface.
func (ts tokenSource) Token() (*oauth2.Token, error) {
	conf := ts.conf

	credSource, err := conf.parse(ts.ctx)
	if err != nil {
		return nil, err
	}
	subjectToken, err := credSource.subjectToken()

	if err != nil {
		return nil, err
	}
	stsRequest := stsexchange.TokenExchangeRequest{
		GrantType:          "urn:ietf:params:oauth:grant-type:token-exchange",
		Audience:           conf.Audience,
		Scope:              conf.Scopes,
		RequestedTokenType: "urn:ietf:params:oauth:token-type:access_token",
		SubjectToken:       subjectToken,
		SubjectTokenType:   conf.SubjectTokenType,
	}
	header := make(http.Header)
	header.Add("Content-Type", "application/x-www-form-urlencoded")
	header.Add("x-goog-api-client", getMetricsHeaderValue(conf, credSource))
	clientAuth := stsexchange.ClientAuthentication{
		AuthStyle:    oauth2.AuthStyleInHeader,
		ClientID:     conf.ClientID,
		ClientSecret: conf.ClientSecret,
	}
	var options map[string]interface{}
	// Do not pass workforce_pool_user_project when client authentication is used.
	// The client ID is sufficient for determining the user project.
	if conf.WorkforcePoolUserProject != "" && conf.ClientID == "" {
		options = map[string]interface{}{
			"userProject": conf.WorkforcePoolUserProject,
		}
	}
	stsResp, err := stsexchange.ExchangeToken(ts.ctx, conf.TokenURL, &stsRequest, clientAuth, header, options)
	if err != nil {
		return nil, err
	}

	accessToken := &oauth2.Token{
		AccessToken: stsResp.AccessToken,
		TokenType:   stsResp.TokenType,
	}
	if stsResp.ExpiresIn < 0 {
		return nil, fmt.Errorf("oauth2/google: got invalid expiry from security token service")
	} else if stsResp.ExpiresIn >= 0 {
		accessToken.Expiry = now().Add(time.Duration(stsResp.ExpiresIn) * time.Second)
	}

	if stsResp.RefreshToken != "" {
		accessToken.RefreshToken = stsResp.RefreshToken
	}
	return accessToken, nil
}