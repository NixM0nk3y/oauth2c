package oauth2

import (
	"crypto/x509"
	"net/http"
	"net/url"

	"github.com/go-jose/go-jose/v3/jwt"
	"github.com/hashicorp/go-multierror"
	"github.com/pkg/errors"
)

type Request struct {
	Method  string
	URL     *url.URL
	Headers map[string][]string
	Form    url.Values
	JARM    map[string]interface{}
	Key     interface{}
	Cert    *x509.Certificate
}

func (r *Request) AuthenticateClient(
	endpoint string,
	mtlsEndpoint string,
	cconfig ClientConfig,
	sconfig ServerConfig,
	hc *http.Client,
) (string, error) {
	var err error

	switch cconfig.AuthMethod {
	case ClientSecretPostAuthMethod:
		r.Form.Set("client_id", cconfig.ClientID)
		r.Form.Set("client_secret", cconfig.ClientSecret)
	case ClientSecretJwtAuthMethod:
		var clientAssertion string

		if clientAssertion, r.Key, err = SignJWT(
			ClientAssertionClaims(sconfig, cconfig),
			SecretSigner([]byte(cconfig.ClientSecret)),
		); err != nil {
			return endpoint, err
		}

		r.Form.Set("client_assertion_type", JwtBearerClientAssertion)
		r.Form.Set("client_assertion", clientAssertion)
	case PrivateKeyJwtAuthMethod:
		var clientAssertion string

		if clientAssertion, r.Key, err = SignJWT(
			ClientAssertionClaims(sconfig, cconfig),
			JWKSigner(cconfig, hc),
		); err != nil {
			return endpoint, err
		}

		r.Form.Set("client_assertion_type", JwtBearerClientAssertion)
		r.Form.Set("client_assertion", clientAssertion)
	case TLSClientAuthMethod, SelfSignedTLSAuthMethod:
		r.Form.Set("client_id", cconfig.ClientID)
		endpoint = mtlsEndpoint

		if tr, ok := hc.Transport.(*http.Transport); ok {
			if len(tr.TLSClientConfig.Certificates) > 0 {
				r.Cert, _ = x509.ParseCertificate(tr.TLSClientConfig.Certificates[0].Certificate[0])
			}
		}
	}

	return endpoint, nil
}

func (r *Request) Get(key string) string {
	if v, ok := r.JARM[key].(string); ok {
		return v
	}

	if v := r.URL.Query().Get(key); v != "" {
		return v
	}

	return r.Form.Get(key)
}

func (r *Request) ParseJARM(signingKey interface{}, encryptionKey interface{}) error {
	var (
		response    = r.Get("response")
		token       *jwt.JSONWebToken
		nestedToken *jwt.NestedJSONWebToken
		err         error
		err2        error
	)

	r.JARM = map[string]interface{}{}

	if response != "" {
		if nestedToken, err = jwt.ParseSignedAndEncrypted(response); err != nil {
			if token, err2 = jwt.ParseSigned(response); err2 != nil {
				return errors.Wrapf(multierror.Append(err, err2), "failed to parse JARM response")
			}
		} else if encryptionKey != nil {
			if token, err = nestedToken.Decrypt(encryptionKey); err != nil {
				return errors.Wrapf(err, "failed to decrypt encrypted JARM response")
			}
		} else {
			return errors.New("no encryption key path")
		}

		if signingKey == nil {
			return errors.New("no signing key path")
		}

		if err = token.Claims(signingKey, &r.JARM); err != nil {
			return err
		}
	}

	return nil
}
