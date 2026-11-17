package services

// test_shared_mocks_test.go holds mock types that are shared across multiple test
// files in this package. These types were originally declared in
// trading_engine_test.go; they have been moved here so that the build tag applied
// to trading_engine_test.go (which guards its broken NewTradingEngine calls) does
// not remove the mocks from the test binary.
//
// GetPortfolioOverview has been added to MockGCTClient here because it was added
// to the gct.GCTClient interface after trading_engine_test.go was last updated.

import (
	"context"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/mock"
	"google.golang.org/grpc"

	"github.com/rantipay/trading-mcp/internal/domain"
	"github.com/rantipay/trading-mcp/internal/gct"
)

// MockGCTClient implements gct.GCTClient for testing.
type MockGCTClient struct {
	mock.Mock
}

func (m *MockGCTClient) Connect(ctx context.Context, address string) error {
	args := m.Called(ctx, address)
	return args.Error(0)
}

func (m *MockGCTClient) Disconnect() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockGCTClient) IsConnected() bool {
	args := m.Called()
	return args.Bool(0)
}

func (m *MockGCTClient) HealthCheck(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

func (m *MockGCTClient) GetTicker(ctx context.Context, exchange, pair string) (*gct.Ticker, error) {
	args := m.Called(ctx, exchange, pair)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*gct.Ticker), args.Error(1)
}

func (m *MockGCTClient) GetOrderbook(ctx context.Context, exchange, pair string) (*gct.Orderbook, error) {
	args := m.Called(ctx, exchange, pair)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*gct.Orderbook), args.Error(1)
}

func (m *MockGCTClient) GetCandles(ctx context.Context, exchange, pair, interval string, limit int) ([]*gct.Candle, error) {
	args := m.Called(ctx, exchange, pair, interval, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*gct.Candle), args.Error(1)
}

func (m *MockGCTClient) GetAccountInfo(ctx context.Context, exchange string) (*gct.AccountInfo, error) {
	args := m.Called(ctx, exchange)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*gct.AccountInfo), args.Error(1)
}

func (m *MockGCTClient) SubmitOrder(ctx context.Context, req *gct.SubmitOrderRequest) (*gct.Order, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*gct.Order), args.Error(1)
}

func (m *MockGCTClient) GetOrderInfo(ctx context.Context, exchange, orderID string) (*gct.Order, error) {
	args := m.Called(ctx, exchange, orderID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*gct.Order), args.Error(1)
}

func (m *MockGCTClient) CancelOrder(ctx context.Context, exchange, orderID string) error {
	args := m.Called(ctx, exchange, orderID)
	return args.Error(0)
}

func (m *MockGCTClient) GetPortfolio(ctx context.Context) (*gct.Portfolio, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*gct.Portfolio), args.Error(1)
}

func (m *MockGCTClient) GetEnabledExchanges(ctx context.Context) ([]string, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]string), args.Error(1)
}

func (m *MockGCTClient) GetOrders(ctx context.Context, exchange, pair, orderType, assetType string, startTime, endTime *time.Time) ([]*gct.Order, error) {
	args := m.Called(ctx, exchange, pair, orderType, assetType, startTime, endTime)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*gct.Order), args.Error(1)
}

func (m *MockGCTClient) GetTickerBySymbol(ctx context.Context, exchange, symbol, assetType string) (*gct.Ticker, error) {
	args := m.Called(ctx, exchange, symbol, assetType)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*gct.Ticker), args.Error(1)
}

func (m *MockGCTClient) GetOrderBookBySymbol(ctx context.Context, exchange, symbol, assetType string) (*gct.Orderbook, error) {
	args := m.Called(ctx, exchange, symbol, assetType)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*gct.Orderbook), args.Error(1)
}

func (m *MockGCTClient) GetTrades(ctx context.Context, exchange, symbol string, limit int) ([]map[string]interface{}, error) {
	args := m.Called(ctx, exchange, symbol, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]map[string]interface{}), args.Error(1)
}

func (m *MockGCTClient) GetOHLCV(ctx context.Context, exchange, symbol, interval string, limit int) ([]map[string]interface{}, error) {
	args := m.Called(ctx, exchange, symbol, interval, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]map[string]interface{}), args.Error(1)
}

func (m *MockGCTClient) CalculateRSI(ctx context.Context, exchange, symbol string, period int) (float64, error) {
	args := m.Called(ctx, exchange, symbol, period)
	return args.Get(0).(float64), args.Error(1)
}

func (m *MockGCTClient) CalculateMACD(ctx context.Context, exchange, symbol string, fastPeriod, slowPeriod, signalPeriod int) (map[string]float64, error) {
	args := m.Called(ctx, exchange, symbol, fastPeriod, slowPeriod, signalPeriod)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(map[string]float64), args.Error(1)
}

func (m *MockGCTClient) CalculateBollingerBands(ctx context.Context, exchange, symbol string, period int, stdDev float64) (map[string]float64, error) {
	args := m.Called(ctx, exchange, symbol, period, stdDev)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(map[string]float64), args.Error(1)
}

func (m *MockGCTClient) GetExchangeInfo(ctx context.Context, exchange string) (map[string]interface{}, error) {
	args := m.Called(ctx, exchange)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(map[string]interface{}), args.Error(1)
}

func (m *MockGCTClient) GetAllTickers(ctx context.Context, exchange string) ([]map[string]interface{}, error) {
	args := m.Called(ctx, exchange)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]map[string]interface{}), args.Error(1)
}

func (m *MockGCTClient) SimulateOrder(ctx context.Context, req *gct.SubmitOrderRequest) (map[string]interface{}, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(map[string]interface{}), args.Error(1)
}

func (m *MockGCTClient) GetBalance(ctx context.Context, exchange, currency string) (map[string]interface{}, error) {
	args := m.Called(ctx, exchange, currency)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(map[string]interface{}), args.Error(1)
}

func (m *MockGCTClient) GetPositions(ctx context.Context, exchange string) ([]map[string]interface{}, error) {
	args := m.Called(ctx, exchange)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]map[string]interface{}), args.Error(1)
}

func (m *MockGCTClient) PlaceOrder(ctx context.Context, req *gct.SubmitOrderRequest) (*gct.Order, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*gct.Order), args.Error(1)
}

func (m *MockGCTClient) GetGRPCConn() *grpc.ClientConn {
	args := m.Called()
	if args.Get(0) == nil {
		return nil
	}
	return args.Get(0).(*grpc.ClientConn)
}

func (m *MockGCTClient) SetKuCoinCredentials(apiKey, apiSecret, apiPassword string) {
	m.Called(apiKey, apiSecret, apiPassword)
}

func (m *MockGCTClient) SubmitFuturesOrder(ctx context.Context, req *gct.SubmitOrderRequest) (*gct.Order, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*gct.Order), args.Error(1)
}

func (m *MockGCTClient) GetFuturesPositions(ctx context.Context, exchange string) ([]*gct.Position, error) {
	args := m.Called(ctx, exchange)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*gct.Position), args.Error(1)
}

func (m *MockGCTClient) GetFuturesPosition(ctx context.Context, exchange, symbol string) (*gct.Position, error) {
	args := m.Called(ctx, exchange, symbol)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*gct.Position), args.Error(1)
}

func (m *MockGCTClient) CloseFuturesPosition(ctx context.Context, exchange, symbol string) error {
	args := m.Called(ctx, exchange, symbol)
	return args.Error(0)
}

func (m *MockGCTClient) SetLeverage(ctx context.Context, exchange, symbol string, leverage int) error {
	args := m.Called(ctx, exchange, symbol, leverage)
	return args.Error(0)
}

func (m *MockGCTClient) ConvertSymbol(symbol string, marketType gct.MarketType, exchange string) (string, error) {
	args := m.Called(symbol, marketType, exchange)
	return args.String(0), args.Error(1)
}

// GetPortfolioOverview was added to the gct.GCTClient interface after
// trading_engine_test.go was last updated. It is implemented here so that
// MockGCTClient satisfies the full interface.
func (m *MockGCTClient) GetPortfolioOverview(ctx context.Context, exchange string) (*gct.PortfolioOverview, error) {
	args := m.Called(ctx, exchange)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*gct.PortfolioOverview), args.Error(1)
}

// MockCacheService implements domain.CacheService for testing.
// Originally declared in market_data_service_test.go (now guarded by build tag).
type MockCacheService struct {
	mock.Mock
}

func (m *MockCacheService) Get(ctx context.Context, key string) (interface{}, error) {
	args := m.Called(ctx, key)
	return args.Get(0), args.Error(1)
}

func (m *MockCacheService) Set(ctx context.Context, key string, value interface{}, ttl time.Duration) error {
	args := m.Called(ctx, key, value, ttl)
	return args.Error(0)
}

func (m *MockCacheService) Delete(ctx context.Context, key string) error {
	args := m.Called(ctx, key)
	return args.Error(0)
}

func (m *MockCacheService) Exists(ctx context.Context, key string) (bool, error) {
	args := m.Called(ctx, key)
	return args.Bool(0), args.Error(1)
}

func (m *MockCacheService) Publish(ctx context.Context, channel string, message interface{}) error {
	args := m.Called(ctx, channel, message)
	return args.Error(0)
}

func (m *MockCacheService) Subscribe(ctx context.Context, channel string) (<-chan interface{}, error) {
	args := m.Called(ctx, channel)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(<-chan interface{}), args.Error(1)
}

// MockOrderRepository implements domain.OrderRepository for testing.
type MockOrderRepository struct {
	mock.Mock
}

func (m *MockOrderRepository) Create(ctx context.Context, order *domain.Order) error {
	args := m.Called(ctx, order)
	return args.Error(0)
}

func (m *MockOrderRepository) GetByID(ctx context.Context, id string) (*domain.Order, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Order), args.Error(1)
}

func (m *MockOrderRepository) GetByUserID(ctx context.Context, userID string, filter *domain.OrderFilter) ([]*domain.Order, error) {
	args := m.Called(ctx, userID, filter)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.Order), args.Error(1)
}

func (m *MockOrderRepository) Update(ctx context.Context, order *domain.Order) error {
	args := m.Called(ctx, order)
	return args.Error(0)
}

func (m *MockOrderRepository) Delete(ctx context.Context, id string) error {
	args := m.Called(ctx, id)
	return args.Error(0)
}

// MockPositionRepository implements domain.PositionRepository for testing.
type MockPositionRepository struct {
	mock.Mock
}

func (m *MockPositionRepository) Create(ctx context.Context, position *domain.Position) error {
	args := m.Called(ctx, position)
	return args.Error(0)
}

func (m *MockPositionRepository) GetByID(ctx context.Context, id string) (*domain.Position, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Position), args.Error(1)
}

func (m *MockPositionRepository) GetByUserID(ctx context.Context, userID string) ([]*domain.Position, error) {
	args := m.Called(ctx, userID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.Position), args.Error(1)
}

func (m *MockPositionRepository) Update(ctx context.Context, position *domain.Position) error {
	args := m.Called(ctx, position)
	return args.Error(0)
}

func (m *MockPositionRepository) Delete(ctx context.Context, id string) error {
	args := m.Called(ctx, id)
	return args.Error(0)
}

// MockBalanceRepository implements domain.BalanceRepository for testing.
type MockBalanceRepository struct {
	mock.Mock
}

func (m *MockBalanceRepository) Create(ctx context.Context, balance *domain.Balance) error {
	args := m.Called(ctx, balance)
	return args.Error(0)
}

func (m *MockBalanceRepository) GetByUserAndExchange(ctx context.Context, userID, exchangeID string) ([]*domain.Balance, error) {
	args := m.Called(ctx, userID, exchangeID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.Balance), args.Error(1)
}

func (m *MockBalanceRepository) Update(ctx context.Context, balance *domain.Balance) error {
	args := m.Called(ctx, balance)
	return args.Error(0)
}

func (m *MockBalanceRepository) Delete(ctx context.Context, userID, exchangeID, asset string) error {
	args := m.Called(ctx, userID, exchangeID, asset)
	return args.Error(0)
}

// createTestLogger returns a silent logrus logger suitable for tests.
func createTestLogger() *logrus.Logger {
	log := logrus.New()
	log.SetLevel(logrus.PanicLevel) // suppress all output during tests
	return log
}
