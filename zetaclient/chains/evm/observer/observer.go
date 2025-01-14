package observer

import (
	"context"
	"fmt"
	"math"
	"math/big"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	ethcommon "github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rlp"
	lru "github.com/hashicorp/golang-lru"
	"github.com/onrik/ethrpc"
	"github.com/pkg/errors"
	"github.com/rs/zerolog"
	"github.com/zeta-chain/protocol-contracts/pkg/contracts/evm/erc20custody.sol"
	"github.com/zeta-chain/protocol-contracts/pkg/contracts/evm/zeta.non-eth.sol"
	zetaconnectoreth "github.com/zeta-chain/protocol-contracts/pkg/contracts/evm/zetaconnector.eth.sol"
	"github.com/zeta-chain/protocol-contracts/pkg/contracts/evm/zetaconnector.non-eth.sol"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/zeta-chain/zetacore/pkg/chains"
	"github.com/zeta-chain/zetacore/pkg/proofs"
	observertypes "github.com/zeta-chain/zetacore/x/observer/types"
	"github.com/zeta-chain/zetacore/zetaclient/chains/evm"
	"github.com/zeta-chain/zetacore/zetaclient/chains/interfaces"
	clientcommon "github.com/zeta-chain/zetacore/zetaclient/common"
	"github.com/zeta-chain/zetacore/zetaclient/config"
	clientcontext "github.com/zeta-chain/zetacore/zetaclient/context"
	"github.com/zeta-chain/zetacore/zetaclient/metrics"
	clienttypes "github.com/zeta-chain/zetacore/zetaclient/types"
)

// Logger is the logger for evm chains
// TODO: Merge this logger with the one in bitcoin
// https://github.com/zeta-chain/node/issues/2022
type Logger struct {
	// Chain is the parent logger for the chain
	Chain zerolog.Logger

	// Inbound is the logger for incoming transactions
	Inbound zerolog.Logger

	// Outbound is the logger for outgoing transactions
	Outbound zerolog.Logger

	// GasPrice is the logger for gas prices
	GasPrice zerolog.Logger

	// Compliance is the logger for compliance checks
	Compliance zerolog.Logger
}

var _ interfaces.ChainObserver = &Observer{}

// Observer is the observer for evm chains
type Observer struct {
	Tss interfaces.TSSSigner

	Mu *sync.Mutex

	chain                         chains.Chain
	evmClient                     interfaces.EVMRPCClient
	evmJSONRPC                    interfaces.EVMJSONRPCClient
	zetacoreClient                interfaces.ZetacoreClient
	lastBlockScanned              uint64
	lastBlock                     uint64
	db                            *gorm.DB
	outboundPendingTransactions   map[string]*ethtypes.Transaction
	outboundConfirmedReceipts     map[string]*ethtypes.Receipt
	outboundConfirmedTransactions map[string]*ethtypes.Transaction
	stop                          chan struct{}
	logger                        Logger
	coreContext                   *clientcontext.ZetacoreContext
	chainParams                   observertypes.ChainParams
	ts                            *metrics.TelemetryServer

	blockCache  *lru.Cache
	headerCache *lru.Cache
}

// NewObserver returns a new EVM chain observer
func NewObserver(
	appContext *clientcontext.AppContext,
	zetacoreClient interfaces.ZetacoreClient,
	tss interfaces.TSSSigner,
	dbpath string,
	loggers clientcommon.ClientLogger,
	evmCfg config.EVMConfig,
	ts *metrics.TelemetryServer,
) (*Observer, error) {
	ob := Observer{
		ts: ts,
	}

	chainLogger := loggers.Std.With().Str("chain", evmCfg.Chain.ChainName.String()).Logger()
	ob.logger = Logger{
		Chain:      chainLogger,
		Inbound:    chainLogger.With().Str("module", "WatchInbound").Logger(),
		Outbound:   chainLogger.With().Str("module", "WatchOutbound").Logger(),
		GasPrice:   chainLogger.With().Str("module", "WatchGasPrice").Logger(),
		Compliance: loggers.Compliance,
	}

	ob.coreContext = appContext.ZetacoreContext()
	chainParams, found := ob.coreContext.GetEVMChainParams(evmCfg.Chain.ChainId)
	if !found {
		return nil, fmt.Errorf("evm chains params not initialized for chain %d", evmCfg.Chain.ChainId)
	}

	ob.chainParams = *chainParams
	ob.stop = make(chan struct{})
	ob.chain = evmCfg.Chain
	ob.Mu = &sync.Mutex{}
	ob.zetacoreClient = zetacoreClient
	ob.Tss = tss
	ob.outboundPendingTransactions = make(map[string]*ethtypes.Transaction)
	ob.outboundConfirmedReceipts = make(map[string]*ethtypes.Receipt)
	ob.outboundConfirmedTransactions = make(map[string]*ethtypes.Transaction)

	ob.logger.Chain.Info().Msgf("Chain %s endpoint %s", ob.chain.ChainName.String(), evmCfg.Endpoint)
	client, err := ethclient.Dial(evmCfg.Endpoint)
	if err != nil {
		ob.logger.Chain.Error().Err(err).Msg("eth Client Dial")
		return nil, err
	}

	ob.evmClient = client
	ob.evmJSONRPC = ethrpc.NewEthRPC(evmCfg.Endpoint)

	// create block header and block caches
	ob.blockCache, err = lru.New(1000)
	if err != nil {
		ob.logger.Chain.Error().Err(err).Msg("failed to create block cache")
		return nil, err
	}

	ob.headerCache, err = lru.New(1000)
	if err != nil {
		ob.logger.Chain.Error().Err(err).Msg("failed to create header cache")
		return nil, err
	}

	err = ob.LoadDB(dbpath, ob.chain)
	if err != nil {
		return nil, err
	}

	ob.logger.Chain.Info().Msgf("%s: start scanning from block %d", ob.chain.String(), ob.GetLastBlockHeightScanned())

	return &ob, nil
}

// WithChain attaches a new chain to the observer
func (ob *Observer) WithChain(chain chains.Chain) {
	ob.Mu.Lock()
	defer ob.Mu.Unlock()
	ob.chain = chain
}

// WithLogger attaches a new logger to the observer
func (ob *Observer) WithLogger(logger zerolog.Logger) {
	ob.Mu.Lock()
	defer ob.Mu.Unlock()
	ob.logger = Logger{
		Chain:    logger,
		Inbound:  logger.With().Str("module", "WatchInbound").Logger(),
		Outbound: logger.With().Str("module", "WatchOutbound").Logger(),
		GasPrice: logger.With().Str("module", "WatchGasPrice").Logger(),
	}
}

// WithEvmClient attaches a new evm client to the observer
func (ob *Observer) WithEvmClient(client interfaces.EVMRPCClient) {
	ob.Mu.Lock()
	defer ob.Mu.Unlock()
	ob.evmClient = client
}

// WithEvmJSONRPC attaches a new evm json rpc client to the observer
func (ob *Observer) WithEvmJSONRPC(client interfaces.EVMJSONRPCClient) {
	ob.Mu.Lock()
	defer ob.Mu.Unlock()
	ob.evmJSONRPC = client
}

// WithZetacoreClient attaches a new client to interact with zetacore to the observer
func (ob *Observer) WithZetacoreClient(client interfaces.ZetacoreClient) {
	ob.Mu.Lock()
	defer ob.Mu.Unlock()
	ob.zetacoreClient = client
}

// WithBlockCache attaches a new block cache to the observer
func (ob *Observer) WithBlockCache(cache *lru.Cache) {
	ob.Mu.Lock()
	defer ob.Mu.Unlock()
	ob.blockCache = cache
}

// Chain returns the chain for the observer
func (ob *Observer) Chain() chains.Chain {
	ob.Mu.Lock()
	defer ob.Mu.Unlock()
	return ob.chain
}

// SetChainParams sets the chain params for the observer
func (ob *Observer) SetChainParams(params observertypes.ChainParams) {
	ob.Mu.Lock()
	defer ob.Mu.Unlock()
	ob.chainParams = params
}

// GetChainParams returns the chain params for the observer
func (ob *Observer) GetChainParams() observertypes.ChainParams {
	ob.Mu.Lock()
	defer ob.Mu.Unlock()
	return ob.chainParams
}

func (ob *Observer) GetConnectorContract() (ethcommon.Address, *zetaconnector.ZetaConnectorNonEth, error) {
	addr := ethcommon.HexToAddress(ob.GetChainParams().ConnectorContractAddress)
	contract, err := FetchConnectorContract(addr, ob.evmClient)
	return addr, contract, err
}

func (ob *Observer) GetConnectorContractEth() (ethcommon.Address, *zetaconnectoreth.ZetaConnectorEth, error) {
	addr := ethcommon.HexToAddress(ob.GetChainParams().ConnectorContractAddress)
	contract, err := FetchConnectorContractEth(addr, ob.evmClient)
	return addr, contract, err
}

func (ob *Observer) GetZetaTokenNonEthContract() (ethcommon.Address, *zeta.ZetaNonEth, error) {
	addr := ethcommon.HexToAddress(ob.GetChainParams().ZetaTokenContractAddress)
	contract, err := FetchZetaZetaNonEthTokenContract(addr, ob.evmClient)
	return addr, contract, err
}

func (ob *Observer) GetERC20CustodyContract() (ethcommon.Address, *erc20custody.ERC20Custody, error) {
	addr := ethcommon.HexToAddress(ob.GetChainParams().Erc20CustodyContractAddress)
	contract, err := FetchERC20CustodyContract(addr, ob.evmClient)
	return addr, contract, err
}

func FetchConnectorContract(
	addr ethcommon.Address,
	client interfaces.EVMRPCClient,
) (*zetaconnector.ZetaConnectorNonEth, error) {
	return zetaconnector.NewZetaConnectorNonEth(addr, client)
}

func FetchConnectorContractEth(
	addr ethcommon.Address,
	client interfaces.EVMRPCClient,
) (*zetaconnectoreth.ZetaConnectorEth, error) {
	return zetaconnectoreth.NewZetaConnectorEth(addr, client)
}

func FetchZetaZetaNonEthTokenContract(
	addr ethcommon.Address,
	client interfaces.EVMRPCClient,
) (*zeta.ZetaNonEth, error) {
	return zeta.NewZetaNonEth(addr, client)
}

func FetchERC20CustodyContract(
	addr ethcommon.Address,
	client interfaces.EVMRPCClient,
) (*erc20custody.ERC20Custody, error) {
	return erc20custody.NewERC20Custody(addr, client)
}

// Start all observation routines for the evm chain
func (ob *Observer) Start() {
	// watch evm chain for incoming txs and post votes to zetacore
	go ob.WatchInbound()

	// watch evm chain for outgoing txs status
	go ob.WatchOutbound()

	// watch evm chain for gas prices and post to zetacore
	go ob.WatchGasPrice()

	// watch zetacore for inbound trackers
	go ob.WatchInboundTracker()

	// watch the RPC status of the evm chain
	go ob.WatchRPCStatus()
}

// WatchRPCStatus watches the RPC status of the evm chain
func (ob *Observer) WatchRPCStatus() {
	ob.logger.Chain.Info().Msgf("Starting RPC status check for chain %s", ob.chain.String())
	ticker := time.NewTicker(60 * time.Second)
	for {
		select {
		case <-ticker.C:
			if !ob.GetChainParams().IsSupported {
				continue
			}
			bn, err := ob.evmClient.BlockNumber(context.Background())
			if err != nil {
				ob.logger.Chain.Error().Err(err).Msg("RPC Status Check error: RPC down?")
				continue
			}
			gasPrice, err := ob.evmClient.SuggestGasPrice(context.Background())
			if err != nil {
				ob.logger.Chain.Error().Err(err).Msg("RPC Status Check error: RPC down?")
				continue
			}
			header, err := ob.evmClient.HeaderByNumber(context.Background(), new(big.Int).SetUint64(bn))
			if err != nil {
				ob.logger.Chain.Error().Err(err).Msg("RPC Status Check error: RPC down?")
				continue
			}
			// #nosec G701 always in range
			blockTime := time.Unix(int64(header.Time), 0).UTC()
			elapsedSeconds := time.Since(blockTime).Seconds()
			if elapsedSeconds > 100 {
				ob.logger.Chain.Warn().
					Msgf("RPC Status Check warning: RPC stale or chain stuck (check explorer)? Latest block %d timestamp is %.0fs ago", bn, elapsedSeconds)
				continue
			}
			ob.logger.Chain.Info().
				Msgf("[OK] RPC status: latest block num %d, timestamp %s ( %.0fs ago), suggested gas price %d", header.Number, blockTime.String(), elapsedSeconds, gasPrice.Uint64())
		case <-ob.stop:
			return
		}
	}
}

func (ob *Observer) Stop() {
	ob.logger.Chain.Info().Msgf("ob %s is stopping", ob.chain.String())
	close(ob.stop) // this notifies all goroutines to stop

	ob.logger.Chain.Info().Msg("closing ob.db")
	dbInst, err := ob.db.DB()
	if err != nil {
		ob.logger.Chain.Info().Msg("error getting database instance")
	}
	err = dbInst.Close()
	if err != nil {
		ob.logger.Chain.Error().Err(err).Msg("error closing database")
	}

	ob.logger.Chain.Info().Msgf("%s observer stopped", ob.chain.String())
}

// SetPendingTx sets the pending transaction in memory
func (ob *Observer) SetPendingTx(nonce uint64, transaction *ethtypes.Transaction) {
	ob.Mu.Lock()
	defer ob.Mu.Unlock()
	ob.outboundPendingTransactions[ob.GetTxID(nonce)] = transaction
}

// GetPendingTx gets the pending transaction from memory
func (ob *Observer) GetPendingTx(nonce uint64) *ethtypes.Transaction {
	ob.Mu.Lock()
	defer ob.Mu.Unlock()
	return ob.outboundPendingTransactions[ob.GetTxID(nonce)]
}

// SetTxNReceipt sets the receipt and transaction in memory
func (ob *Observer) SetTxNReceipt(nonce uint64, receipt *ethtypes.Receipt, transaction *ethtypes.Transaction) {
	ob.Mu.Lock()
	defer ob.Mu.Unlock()
	delete(ob.outboundPendingTransactions, ob.GetTxID(nonce)) // remove pending transaction, if any
	ob.outboundConfirmedReceipts[ob.GetTxID(nonce)] = receipt
	ob.outboundConfirmedTransactions[ob.GetTxID(nonce)] = transaction
}

// GetTxNReceipt gets the receipt and transaction from memory
func (ob *Observer) GetTxNReceipt(nonce uint64) (*ethtypes.Receipt, *ethtypes.Transaction) {
	ob.Mu.Lock()
	defer ob.Mu.Unlock()
	receipt := ob.outboundConfirmedReceipts[ob.GetTxID(nonce)]
	transaction := ob.outboundConfirmedTransactions[ob.GetTxID(nonce)]
	return receipt, transaction
}

// IsTxConfirmed returns true if there is a confirmed tx for 'nonce'
func (ob *Observer) IsTxConfirmed(nonce uint64) bool {
	ob.Mu.Lock()
	defer ob.Mu.Unlock()
	return ob.outboundConfirmedReceipts[ob.GetTxID(nonce)] != nil &&
		ob.outboundConfirmedTransactions[ob.GetTxID(nonce)] != nil
}

// CheckTxInclusion returns nil only if tx is included at the position indicated by the receipt ([block, index])
func (ob *Observer) CheckTxInclusion(tx *ethtypes.Transaction, receipt *ethtypes.Receipt) error {
	block, err := ob.GetBlockByNumberCached(receipt.BlockNumber.Uint64())
	if err != nil {
		return errors.Wrapf(err, "GetBlockByNumberCached error for block %d txHash %s nonce %d",
			receipt.BlockNumber.Uint64(), tx.Hash(), tx.Nonce())
	}

	// #nosec G701 non negative value
	if receipt.TransactionIndex >= uint(len(block.Transactions)) {
		return fmt.Errorf("transaction index %d out of range [0, %d), txHash %s nonce %d block %d",
			receipt.TransactionIndex, len(block.Transactions), tx.Hash(), tx.Nonce(), receipt.BlockNumber.Uint64())
	}

	txAtIndex := block.Transactions[receipt.TransactionIndex]
	if !strings.EqualFold(txAtIndex.Hash, tx.Hash().Hex()) {
		ob.RemoveCachedBlock(receipt.BlockNumber.Uint64()) // clean stale block from cache
		return fmt.Errorf("transaction at index %d has different hash %s, txHash %s nonce %d block %d",
			receipt.TransactionIndex, txAtIndex.Hash, tx.Hash(), tx.Nonce(), receipt.BlockNumber.Uint64())
	}

	return nil
}

// SetLastBlockHeightScanned set last block height scanned (not necessarily caught up with external block; could be slow/paused)
func (ob *Observer) SetLastBlockHeightScanned(height uint64) {
	atomic.StoreUint64(&ob.lastBlockScanned, height)
	metrics.LastScannedBlockNumber.WithLabelValues(ob.chain.ChainName.String()).Set(float64(height))
}

// GetLastBlockHeightScanned get last block height scanned (not necessarily caught up with external block; could be slow/paused)
func (ob *Observer) GetLastBlockHeightScanned() uint64 {
	height := atomic.LoadUint64(&ob.lastBlockScanned)
	return height
}

// SetLastBlockHeight set external last block height
func (ob *Observer) SetLastBlockHeight(height uint64) {
	atomic.StoreUint64(&ob.lastBlock, height)
}

// GetLastBlockHeight get external last block height
func (ob *Observer) GetLastBlockHeight() uint64 {
	return atomic.LoadUint64(&ob.lastBlock)
}

// WatchGasPrice watches evm chain for gas prices and post to zetacore
func (ob *Observer) WatchGasPrice() {
	// report gas price right away as the ticker takes time to kick in
	err := ob.PostGasPrice()
	if err != nil {
		ob.logger.GasPrice.Error().Err(err).Msgf("PostGasPrice error for chain %d", ob.chain.ChainId)
	}

	// start gas price ticker
	ticker, err := clienttypes.NewDynamicTicker(
		fmt.Sprintf("EVM_WatchGasPrice_%d", ob.chain.ChainId),
		ob.GetChainParams().GasPriceTicker,
	)
	if err != nil {
		ob.logger.GasPrice.Error().Err(err).Msg("NewDynamicTicker error")
		return
	}
	ob.logger.GasPrice.Info().Msgf("WatchGasPrice started for chain %d with interval %d",
		ob.chain.ChainId, ob.GetChainParams().GasPriceTicker)

	defer ticker.Stop()
	for {
		select {
		case <-ticker.C():
			if !ob.GetChainParams().IsSupported {
				continue
			}
			err = ob.PostGasPrice()
			if err != nil {
				ob.logger.GasPrice.Error().Err(err).Msgf("PostGasPrice error for chain %d", ob.chain.ChainId)
			}
			ticker.UpdateInterval(ob.GetChainParams().GasPriceTicker, ob.logger.GasPrice)
		case <-ob.stop:
			ob.logger.GasPrice.Info().Msg("WatchGasPrice stopped")
			return
		}
	}
}

func (ob *Observer) PostGasPrice() error {

	// GAS PRICE
	gasPrice, err := ob.evmClient.SuggestGasPrice(context.TODO())
	if err != nil {
		ob.logger.GasPrice.Err(err).Msg("Err SuggestGasPrice:")
		return err
	}
	blockNum, err := ob.evmClient.BlockNumber(context.TODO())
	if err != nil {
		ob.logger.GasPrice.Err(err).Msg("Err Fetching Most recent Block : ")
		return err
	}

	// SUPPLY
	supply := "100" // lockedAmount on ETH, totalSupply on other chains

	zetaHash, err := ob.zetacoreClient.PostGasPrice(ob.chain, gasPrice.Uint64(), supply, blockNum)
	if err != nil {
		ob.logger.GasPrice.Err(err).Msg("PostGasPrice to zetacore failed")
		return err
	}
	_ = zetaHash

	return nil
}

// TransactionByHash query transaction by hash via JSON-RPC
func (ob *Observer) TransactionByHash(txHash string) (*ethrpc.Transaction, bool, error) {
	tx, err := ob.evmJSONRPC.EthGetTransactionByHash(txHash)
	if err != nil {
		return nil, false, err
	}
	err = evm.ValidateEvmTransaction(tx)
	if err != nil {
		return nil, false, err
	}
	return tx, tx.BlockNumber == nil, nil
}

func (ob *Observer) GetBlockHeaderCached(blockNumber uint64) (*ethtypes.Header, error) {
	if header, ok := ob.headerCache.Get(blockNumber); ok {
		return header.(*ethtypes.Header), nil
	}
	header, err := ob.evmClient.HeaderByNumber(context.Background(), new(big.Int).SetUint64(blockNumber))
	if err != nil {
		return nil, err
	}
	ob.headerCache.Add(blockNumber, header)
	return header, nil
}

// GetBlockByNumberCached get block by number from cache
// returns block, ethrpc.Block, isFallback, isSkip, error
func (ob *Observer) GetBlockByNumberCached(blockNumber uint64) (*ethrpc.Block, error) {
	if block, ok := ob.blockCache.Get(blockNumber); ok {
		return block.(*ethrpc.Block), nil
	}
	if blockNumber > math.MaxInt32 {
		return nil, fmt.Errorf("block number %d is too large", blockNumber)
	}
	// #nosec G701 always in range, checked above
	block, err := ob.BlockByNumber(int(blockNumber))
	if err != nil {
		return nil, err
	}
	ob.blockCache.Add(blockNumber, block)
	return block, nil
}

// RemoveCachedBlock remove block from cache
func (ob *Observer) RemoveCachedBlock(blockNumber uint64) {
	ob.blockCache.Remove(blockNumber)
}

// BlockByNumber query block by number via JSON-RPC
func (ob *Observer) BlockByNumber(blockNumber int) (*ethrpc.Block, error) {
	block, err := ob.evmJSONRPC.EthGetBlockByNumber(blockNumber, true)
	if err != nil {
		return nil, err
	}
	for i := range block.Transactions {
		err := evm.ValidateEvmTransaction(&block.Transactions[i])
		if err != nil {
			return nil, err
		}
	}
	return block, nil
}

// LoadLastScannedBlock loads last scanned block from specified height or from database
// The last scanned block is the height from which the observer should continue scanning for inbound transactions
func (ob *Observer) LoadLastScannedBlock() error {
	// get environment variable
	envvar := ob.chain.ChainName.String() + "_SCAN_FROM"
	scanFromBlock := os.Getenv(envvar)

	// load from environment variable if set
	if scanFromBlock != "" {
		ob.logger.Chain.Info().
			Msgf("LoadLastScannedBlock: envvar %s is set; scan from  block %s", envvar, scanFromBlock)
		if scanFromBlock == clienttypes.EnvVarLatest {
			header, err := ob.evmClient.HeaderByNumber(context.Background(), nil)
			if err != nil {
				return err
			}
			ob.SetLastBlockHeightScanned(header.Number.Uint64())
		} else {
			scanFromBlockInt, err := strconv.ParseUint(scanFromBlock, 10, 64)
			if err != nil {
				return err
			}
			ob.SetLastBlockHeightScanned(scanFromBlockInt)
		}
	} else {
		// load from DB otherwise
		var lastBlock clienttypes.LastBlockSQLType
		if err := ob.db.First(&lastBlock, clienttypes.LastBlockNumID).Error; err != nil {
			ob.logger.Chain.Info().Msg("LoadLastScannedBlock: last scanned block not found in DB, scan from latest")
			header, err := ob.evmClient.HeaderByNumber(context.Background(), nil)
			if err != nil {
				return err
			}
			ob.SetLastBlockHeightScanned(header.Number.Uint64())
			if dbc := ob.db.Save(clienttypes.ToLastBlockSQLType(ob.GetLastBlockHeightScanned())); dbc.Error != nil {
				ob.logger.Chain.Error().Err(dbc.Error).Msgf("LoadLastScannedBlock: error writing last scanned block %d to DB", ob.GetLastBlockHeightScanned())
			}
		} else {
			ob.SetLastBlockHeightScanned(lastBlock.Num)
		}
	}
	ob.logger.Chain.Info().
		Msgf("LoadLastScannedBlock: chain %d starts scanning from block %d", ob.chain.ChainId, ob.GetLastBlockHeightScanned())

	return nil
}

// LoadDB open sql database and load data into EVM observer
func (ob *Observer) LoadDB(dbPath string, chain chains.Chain) error {
	if dbPath != "" {
		if _, err := os.Stat(dbPath); os.IsNotExist(err) {
			err := os.MkdirAll(dbPath, os.ModePerm)
			if err != nil {
				return err
			}
		}
		path := fmt.Sprintf("%s/%s", dbPath, chain.ChainName.String()) //Use "file::memory:?cache=shared" for temp db
		db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
		if err != nil {
			ob.logger.Chain.Error().
				Err(err).
				Msgf("failed to open observer database for %s", ob.chain.ChainName.String())
			return err
		}

		err = db.AutoMigrate(&clienttypes.ReceiptSQLType{},
			&clienttypes.TransactionSQLType{},
			&clienttypes.LastBlockSQLType{})
		if err != nil {
			ob.logger.Chain.Error().Err(err).Msg("error migrating db")
			return err
		}

		ob.db = db
		err = ob.LoadLastScannedBlock()
		if err != nil {
			return err
		}
	}
	return nil
}

func (ob *Observer) postBlockHeader(tip uint64) error {
	bn := tip

	res, err := ob.zetacoreClient.GetBlockHeaderChainState(ob.chain.ChainId)
	if err == nil && res.ChainState != nil && res.ChainState.EarliestHeight > 0 {
		// #nosec G701 always positive
		bn = uint64(res.ChainState.LatestHeight) + 1 // the next header to post
	}

	if bn > tip {
		return fmt.Errorf("postBlockHeader: must post block confirmed block header: %d > %d", bn, tip)
	}

	header, err := ob.GetBlockHeaderCached(bn)
	if err != nil {
		ob.logger.Inbound.Error().Err(err).Msgf("postBlockHeader: error getting block: %d", bn)
		return err
	}
	headerRLP, err := rlp.EncodeToBytes(header)
	if err != nil {
		ob.logger.Inbound.Error().Err(err).Msgf("postBlockHeader: error encoding block header: %d", bn)
		return err
	}

	_, err = ob.zetacoreClient.PostVoteBlockHeader(
		ob.chain.ChainId,
		header.Hash().Bytes(),
		header.Number.Int64(),
		proofs.NewEthereumHeader(headerRLP),
	)
	if err != nil {
		ob.logger.Inbound.Error().Err(err).Msgf("postBlockHeader: error posting block header: %d", bn)
		return err
	}
	return nil
}
