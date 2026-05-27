package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	shttp "github.com/DIMO-Network/shared/pkg/http"
	"github.com/rs/zerolog"
)

var ErrBadRequest = errors.New("bad request")

// IdentityAPI returns raw bytes from the DIMO identity-api so the frontend
// can be proxied transparently without imposing a typed schema in the API.
// (The typed flavour lives in /internal/gateway/identity_api.go.)
type IdentityAPI interface {
	GetDefinitionByID(id string) ([]byte, error)
	GetVehicleByTokenID(id string) ([]byte, error)
	GetOwnerBy0x(owner string, first int, after string) ([]byte, error)
	Query(graphqlQuery string) ([]byte, error)
}

type identityAPIService struct {
	apiURL     string
	httpClient shttp.ClientWrapper
	logger     zerolog.Logger
}

func NewIdentityAPIService(logger zerolog.Logger, identityAPIURL string) IdentityAPI {
	h := map[string]string{"Content-Type": "application/json"}
	hcw, _ := shttp.NewClientWrapper("", "", 10*time.Second, h, false, shttp.WithRetry(3))
	return &identityAPIService{
		httpClient: hcw,
		apiURL:     identityAPIURL,
		logger:     logger,
	}
}

func (i *identityAPIService) GetDefinitionByID(id string) ([]byte, error) {
	query := `{
		deviceDefinition(by: {id: "` + id + `"}) {
			model
			year
			manufacturer { name }
		}
	}`
	return i.Query(query)
}

func (i *identityAPIService) GetOwnerBy0x(owner string, first int, after string) ([]byte, error) {
	afterClause := ""
	if after != "" {
		afterClause = fmt.Sprintf("\n      after: %q", after)
	}
	query := fmt.Sprintf(`{
		vehicles(
			first: %d%s
			filterBy: { owner: "%s" }
		) {
			nodes {
				owner
				tokenId
				definition { make model year }
			}
			pageInfo { startCursor endCursor hasNextPage hasPreviousPage }
		}
	}`, first, afterClause, owner)
	return i.Query(query)
}

func (i *identityAPIService) GetVehicleByTokenID(id string) ([]byte, error) {
	query := `{
		vehicle(tokenId: ` + id + `) {
			id
			owner
			mintedAt
			definition { id make model year }
		}
	}`
	return i.Query(query)
}

func (i *identityAPIService) Query(graphqlQuery string) ([]byte, error) {
	payload, err := json.Marshal(graphQLRequest{Query: graphqlQuery})
	if err != nil {
		return nil, err
	}

	resp, err := i.httpClient.ExecuteRequest(i.apiURL, "POST", payload)
	if err != nil {
		i.logger.Err(err).Msg("Failed to send POST request")
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 400 {
		return nil, ErrBadRequest
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		i.logger.Err(err).Msg("Failed to read response body")
		return nil, err
	}
	return body, nil
}

type graphQLRequest struct {
	Query string `json:"query"`
}
