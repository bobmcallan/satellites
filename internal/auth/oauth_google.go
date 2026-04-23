package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"golang.org/x/oauth2"
)

// googleUserInfoURL is overridable at test time so the integration test can
// point the provider at a mock server.
var googleUserInfoURL = "https://openidconnect.googleapis.com/v1/userinfo"

func fetchGoogleUserInfo(ctx context.Context, token *oauth2.Token) (ProviderUserInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, googleUserInfoURL, nil)
	if err != nil {
		return ProviderUserInfo{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token.AccessToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ProviderUserInfo{}, fmt.Errorf("google userinfo: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ProviderUserInfo{}, fmt.Errorf("google userinfo status %d", resp.StatusCode)
	}
	var body struct {
		Email string `json:"email"`
		Name  string `json:"name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return ProviderUserInfo{}, fmt.Errorf("google userinfo decode: %w", err)
	}
	return ProviderUserInfo{Email: body.Email, DisplayName: body.Name}, nil
}
