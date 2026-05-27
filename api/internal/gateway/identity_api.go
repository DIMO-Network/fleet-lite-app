package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"time"

	"github.com/DIMO-Network/fleet-lite-app/internal/config"
	"github.com/DIMO-Network/fleet-lite-app/internal/models"
	shttp "github.com/DIMO-Network/shared/pkg/http"
	"github.com/patrickmn/go-cache"
	"github.com/rs/zerolog"
)

var ErrBadRequest = errors.New("bad request")

type IdentityAPI interface {
	GetCachedVehicleByTokenID(tokenID int64) (*models.Vehicle, error)
	FetchVehicleByTokenID(tokenID int64) (*models.Vehicle, error)
	FetchVehiclesByWalletAddress(address string) ([]models.Vehicle, error)
}

type identityAPIService struct {
	apiURL     url.URL
	cache      *cache.Cache
	httpClient shttp.ClientWrapper
	logger     zerolog.Logger
}

func NewIdentityAPIService(logger zerolog.Logger, settings *config.Settings) IdentityAPI {
	h := map[string]string{"Content-Type": "application/json"}
	hcw, _ := shttp.NewClientWrapper("", "", 10*time.Second, h, false, shttp.WithRetry(3))
	c := cache.New(10*time.Minute, 15*time.Minute)

	return &identityAPIService{
		httpClient: hcw,
		apiURL:     settings.IdentityAPIEndpoint,
		logger:     logger,
		cache:      c,
	}
}

func (i *identityAPIService) GetCachedVehicleByTokenID(tokenID int64) (*models.Vehicle, error) {
	key := fmt.Sprintf("vehicle_%s", strconv.FormatInt(tokenID, 10))
	if cached, found := i.cache.Get(key); found {
		return cached.(*models.Vehicle), nil
	}
	return nil, errors.New("not found")
}

func (i *identityAPIService) FetchVehicleByTokenID(tokenID int64) (*models.Vehicle, error) {
	strTokenID := strconv.FormatInt(tokenID, 10)
	body, err := i.Query(fmt.Sprintf(VehicleByTokenIDQuery, strTokenID))
	if err != nil {
		return nil, err
	}

	var resp models.GraphQlData[models.SingleVehicle]
	if err = json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}

	i.cache.Set(fmt.Sprintf("vehicle_%s", strTokenID), &resp.Data.Vehicle, cache.DefaultExpiration)
	return &resp.Data.Vehicle, nil
}

func (i *identityAPIService) FetchVehiclesByWalletAddress(walletAddress string) ([]models.Vehicle, error) {
	vehicles := []models.Vehicle{}
	page, err := i.fetchUserVehiclesPage(walletAddress, "")
	if err != nil {
		return nil, err
	}
	vehicles = append(vehicles, page.Nodes...)

	for page.PageInfo.HasNextPage {
		page, err = i.fetchUserVehiclesPage(walletAddress, page.PageInfo.EndCursor)
		if err != nil {
			return nil, err
		}
		vehicles = append(vehicles, page.Nodes...)
	}
	return vehicles, nil
}

func (i *identityAPIService) fetchUserVehiclesPage(walletAddress, after string) (*models.PagedVehiclesNodes, error) {
	afterCursor := "null"
	if after != "" {
		afterCursor = strconv.Quote(after)
	}
	body, err := i.Query(fmt.Sprintf(VehiclesByWalletAndCursorQuery, walletAddress, afterCursor))
	if err != nil {
		return nil, err
	}

	var paged models.GraphQlData[models.PagedVehicles]
	if err = json.Unmarshal(body, &paged); err != nil {
		return nil, err
	}
	return &paged.Data.VehicleNodes, nil
}

func (i *identityAPIService) Query(graphqlQuery string) ([]byte, error) {
	payloadBytes, err := json.Marshal(models.GraphQLRequest{Query: graphqlQuery})
	if err != nil {
		return nil, err
	}

	resp, err := i.httpClient.ExecuteRequest(i.apiURL.String(), "POST", payloadBytes)
	if err != nil {
		i.logger.Err(err).Msg("Failed to send POST request")
		return nil, err
	}
	defer func(b io.ReadCloser) {
		if err := b.Close(); err != nil {
			i.logger.Err(err).Msg("Failed to close response body")
		}
	}(resp.Body)

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
