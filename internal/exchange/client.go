package exchange

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/shopspring/decimal"
)

type Client struct {
	httpClient *http.Client
	url        string
}

func NewClient(url string) *Client {
	return &Client{
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
		url: url,
	}
}

type cbrResponse struct {
	Valute struct {
		USD struct {
			Value json.Number `json:"Value"`
		} `json:"USD"`
	} `json:"Valute"`
}

func (c *Client) GetUSDRUB(
	ctx context.Context,
) (decimal.Decimal, error) {
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		c.url,
		nil,
	)
	if err != nil {
		return decimal.Zero, fmt.Errorf(
			"create exchange rate request: %w",
			err,
		)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return decimal.Zero, fmt.Errorf(
			"request exchange rate: %w",
			err,
		)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return decimal.Zero, fmt.Errorf(
			"exchange rate source returned status %d",
			resp.StatusCode,
		)
	}

	var data cbrResponse

	decoder := json.NewDecoder(resp.Body)
	decoder.UseNumber()

	if err := decoder.Decode(&data); err != nil {
		return decimal.Zero, fmt.Errorf(
			"decode exchange rate response: %w",
			err,
		)
	}

	rate, err := decimal.NewFromString(
		data.Valute.USD.Value.String(),
	)
	if err != nil {
		return decimal.Zero, fmt.Errorf(
			"parse USD/RUB rate: %w",
			err,
		)
	}

	if rate.LessThanOrEqual(decimal.Zero) {
		return decimal.Zero, fmt.Errorf(
			"invalid USD/RUB rate",
		)
	}

	return rate, nil
}
