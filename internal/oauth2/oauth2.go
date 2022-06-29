package oauth2

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/lithammer/shortuuid/v4"
	"github.com/pkg/errors"
)

// grant types
const (
	AuthorizationCodeGrantType string = "authorization_code"
	ClientCredentialsGrantType string = "client_credentials"
	// ImplicitGrantType          string = "implicit"
	// RefreshTokenGrantType      string = "refresh_token"
	// PasswordGrantType          string = "password"
	// JWTBearerGrantType         string = "urn:ietf:params:oauth:grant-type:jwt-bearer"
	// CIBAGrantType              string = "urn:openid:params:grant-type:ciba"
	// TokenExchangeGrantType     string = "urn:ietf:params:oauth:grant-type:token-exchange"
	// DeviceGrantType            string = "urn:ietf:params:oauth:grant-type:device_code"
)

// auth methods
const (
	ClientSecretBasicAuthMethod string = "client_secret_basic"
	ClientSecretPostAuthMethod  string = "client_secret_post"
	// ClientSecretJwtAuthMethod   string = "client_secret_jwt"
	// PrivateKeyJwtAuthMethod     string = "private_key_jwt"
	// SelfSignedTLSAuthMethod     string = "self_signed_tls_client_auth"
	// TLSClientAuthMethod         string = "tls_client_auth"
	// NoneAuthMethod              string = "none"
)

type ClientConfig struct {
	IssuerURL    string
	ClientID     string
	ClientSecret string
	GrantType    string
	AuthMethod   string
}

func RequestAuthorization(addr string, cconfig ClientConfig, sconfig ServerConfig) (r Request, err error) {
	if r.URL, err = url.Parse(sconfig.AuthorizationEndpoint); err != nil {
		return r, errors.Wrapf(err, "failed to parse authorization endpoint")
	}

	values := url.Values{
		"client_id":     {cconfig.ClientID},
		"response_type": {"code"},
		"redirect_uri":  {"http://" + addr + "/callback"},
		"state":         {shortuuid.New()},
		"nonce":         {shortuuid.New()},
	}

	r.URL.RawQuery = values.Encode()
	r.Method = http.MethodGet

	return r, nil
}

func WaitForCallback(addr string) (request Request, err error) {
	var (
		srv = http.Server{Addr: addr}
		wg  sync.WaitGroup
	)

	wg.Add(1)

	http.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		request.Method = r.Method
		request.URL = r.URL

		if r.URL.Query().Get("error") != "" {
			err = &Error{
				ErrorCode:   r.URL.Query().Get("error"),
				Description: r.URL.Query().Get("error_description"),
				Hint:        r.URL.Query().Get("error_hint"),
				TraceID:     r.URL.Query().Get("trace_id"),
			}

			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`Authorization failed. You may close this browser.`))
		} else {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`Authorization succeeded. You may close this browser.`))
		}

		time.AfterFunc(time.Second, func() { srv.Shutdown(context.Background()) })
	})

	go func() {
		defer wg.Done()

		if serr := srv.ListenAndServe(); serr != http.ErrServerClosed {
			err = serr
		}
	}()

	wg.Wait()

	return request, err
}

type TokenResponse struct {
	AccessToken     string `json:"access_token,omitempty"`
	ExpiresIn       int64  `json:"expires_in,omitempty"`
	IDToken         string `json:"id_token,omitempty"`
	IssuedTokenType string `json:"issued_token_type,omitempty"`
	RefreshToken    string `json:"refresh_token,omitempty"`
	Scope           string `json:"scope,omitempty"`
	TokenType       string `json:"token_type,omitempty"`
}

type RequestTokenParams struct {
	Code        string
	RedirectURL string
}

type RequestTokenOption func(*RequestTokenParams)

func WithAuthorizationCode(code string) func(*RequestTokenParams) {
	return func(opts *RequestTokenParams) {
		opts.Code = code
	}
}

func WithRedirectURL(url string) func(*RequestTokenParams) {
	return func(opts *RequestTokenParams) {
		opts.RedirectURL = url
	}
}

func RequestToken(
	ctx context.Context,
	cconfig ClientConfig,
	sconfig ServerConfig,
	hc *http.Client,
	opts ...RequestTokenOption,
) (request Request, response TokenResponse, err error) {
	var (
		req    *http.Request
		resp   *http.Response
		params RequestTokenParams
		body   []byte
	)

	for _, opt := range opts {
		opt(&params)
	}

	request.Form = url.Values{
		"grant_type": {cconfig.GrantType},
	}

	switch cconfig.AuthMethod {
	case ClientSecretPostAuthMethod:
		request.Form.Set("client_id", cconfig.ClientID)
		request.Form.Set("client_secret", cconfig.ClientSecret)
	}

	if params.RedirectURL != "" {
		request.Form.Set("redirect_uri", params.RedirectURL)
	}

	if params.Code != "" {
		request.Form.Set("code", params.Code)
	}

	if req, err = http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		sconfig.TokenEndpoint,
		strings.NewReader(request.Form.Encode()),
	); err != nil {
		return request, response, err
	}

	if cconfig.AuthMethod == ClientSecretBasicAuthMethod {
		req.SetBasicAuth(cconfig.ClientID, cconfig.ClientSecret)
	}

	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")

	request.Method = req.Method
	request.Headers = req.Header
	request.URL = req.URL

	if resp, err = hc.Do(req); err != nil {
		return request, response, err
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return request, response, ParseError(resp)
	}

	if body, err = io.ReadAll(resp.Body); err != nil {
		return request, response, fmt.Errorf("failed to read exchange response body: %w", err)
	}

	if err = json.Unmarshal(body, &response); err != nil {
		return request, response, fmt.Errorf("failed to parse exchange response: %w", err)
	}

	return request, response, nil
}
