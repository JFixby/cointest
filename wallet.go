package coinharness

import (
	"fmt"
	"github.com/jfixby/coin"
	"github.com/jfixby/pin"
)

// Wallet wraps optional test wallet implementations for different test setups
type Wallet interface {
	// Network returns current network of the wallet
	Network() Network

	// NewAddress returns a fresh address spendable by the wallet.
	NewAddress(accountName string) (Address, error)

	// Start wallet process
	Start(args *TestWalletStartArgs) error

	// Stops wallet process gently
	Stop()

	// Dispose releases all resources allocated by the wallet
	// This action is final (irreversible)
	Dispose() error

	// Sync block until the wallet has fully synced up to the desiredHeight
	Sync(desiredHeight int64) int64

	SyncedHeight() int64

	ListUnspent() ([]*Unspent, error)

	// ConfirmedBalance returns wallet balance
	GetBalance() (*GetBalanceResult, error)

	// RPCClient returns node RPCConnection
	RPCClient() *RPCConnection

	GetNewAddress(accountName string) (Address, error)

	CreateNewAccount(accountName string) error

	ValidateAddress(address Address) (*ValidateAddressResult, error)

	WalletUnlock(password string, timeout int64) error

	WalletLock() error

	WalletInfo() (*WalletInfoResult, error)
	ListAccounts() (map[string]coin.Amount, error)

	SendFrom(account string, address Address, amount coin.Amount) error
}

const DefaultAccountName = "default"

type WalletInfoResult struct {
	Unlocked         bool
	DaemonConnected  bool
	TxFee            float64
	TicketFee        float64
	TicketPurchasing bool
	VoteBits         uint16
	VoteBitsExtended string
	VoteVersion      uint32
	Voting           bool
}

type GetBalanceResult struct {
	Balances  map[string]GetAccountBalanceResult
	BlockHash Hash
}

// GetAccountBalanceResult models the account data from the getbalance command.
type GetAccountBalanceResult struct {
	AccountName             string
	ImmatureCoinbaseRewards coin.Amount
	ImmatureStakeGeneration coin.Amount
	LockedByTickets         coin.Amount
	Spendable               coin.Amount
	Total                   coin.Amount
	Unconfirmed             coin.Amount
	VotingAuthority         coin.Amount
}

type ValidateAddressResult struct {
	IsValid      bool
	Address      string
	IsMine       bool
	IsWatchOnly  bool
	IsScript     bool
	PubKeyAddr   string
	PubKey       string
	IsCompressed bool
	Account      string
	Addresses    []string
	Hex          string
	Script       string
	SigsRequired int32
}

// TestWalletFactory produces a new Wallet instance
type TestWalletFactory interface {
	// NewWallet is used by harness builder to setup a wallet component
	NewWallet(cfg *TestWalletConfig) Wallet
}

// TestWalletConfig bundles settings required to create a new wallet instance
type TestWalletConfig struct {
	Seed          Seed //[]byte // chainhash.HashSize + 4
	NodeRPCHost   string
	NodeRPCPort   int
	WalletRPCHost string
	WalletRPCPort int
	ActiveNet     Network
	WorkingDir    string

	NodeUser       string
	NodePassword   string
	WalletUser     string
	WalletPassword string

	//PrivateKeyKeyToAddr  func(key PrivateKey, Net Network) (Address, error)
	//NewMasterKeyFromSeed func(seed Seed, params Network) (ExtendedKey, error)
	//RPCClientFactory     RPCClientFactory
}

// CreateTransactionArgs bundles CreateTransaction() arguments to minimize diff
// in case a new argument for the function is added
type CreateTransactionArgs struct {
	Outputs         []*TxOut
	FeeRate         coin.Amount
	Change          bool
	TxVersion       int32
	PayToAddrScript func(Address) ([]byte, error) // txscript.PayToAddrScript(addr)
	TxSerializeSize func(*MessageTx) int          // *wire.MsgTx.TxSerializeSize()
	Account         string
}

// TestWalletStartArgs bundles Start() arguments to minimize diff
// in case a new argument for the function is added
type TestWalletStartArgs struct {
	NodeRPCCertFile          string
	ExtraArguments           map[string]interface{}
	DebugOutput              bool
	MaxSecondsToWaitOnLaunch int
	NodeRPCConfig            RPCConnectionConfig
}

// CreateTransaction returns a fully signed transaction paying to the specified
// outputs while observing the desired fee rate. The passed fee rate should be
// expressed in satoshis-per-byte. The transaction being created can optionally
// include a change output indicated by the Change boolean.
func CreateTransaction(wallet Wallet, args *CreateTransactionArgs) (*MessageTx, error) {
	unspent, err := wallet.ListUnspent()
	if err != nil {
		return nil, err
	}

	tx := &MessageTx{}

	// Tally up the total amount to be sent in order to perform coin
	// selection shortly below.
	outputAmt := coin.Amount{0}
	for _, output := range args.Outputs {
		outputAmt.AtomsValue += output.Value.AtomsValue
		tx.TxOut = append(tx.TxOut, output)
	}

	// Attempt to fund the transaction with spendable Utxos.
	if err := fundTx(
		wallet,
		args.Account,
		unspent,
		tx,
		outputAmt,
		args.FeeRate,
		args.PayToAddrScript,
		args.TxSerializeSize,
	); err != nil {
		return nil, err
	}

	return tx, nil
}

// fundTx attempts to fund a transaction sending amt coins.  The coins are
// selected such that the final amount spent pays enough fees as dictated by
// the passed fee rate.  The passed fee rate should be expressed in
// atoms-per-byte.
//
// NOTE: The InMemoryWallet's mutex must be held when this function is called.
func fundTx(
	wallet Wallet,
	account string,
	unspent []*Unspent,
	tx *MessageTx,
	amt coin.Amount,
	feeRate coin.Amount,
	PayToAddrScript func(Address) ([]byte, error),
	TxSerializeSize func(*MessageTx) int,
) error {
	const (
		// spendSize is the largest number of bytes of a sigScript
		// which spends a p2pkh output: OP_DATA_73 <sig> OP_DATA_33 <pubkey>
		spendSize = 1 + 73 + 1 + 33
	)

	pin.AssertNotNil("PayToAddrScript", PayToAddrScript)
	pin.AssertNotNil("TxSerializeSize", TxSerializeSize)
	pin.AssertNotEmpty("account", account)

	amtSelected := coin.Amount{0}
	//txSize := int64(0)
	for _, output := range unspent {
		// Skip any outputs that are still currently immature or are
		// currently locked.
		if !output.Spendable {
			continue
		}
		if output.Account != account {
			continue
		}

		amtSelected.AtomsValue += output.Amount.AtomsValue

		// Add the selected output to the transaction, updating the
		// current tx size while accounting for the size of the future
		// sigScript.
		txIn := &TxIn{
			PreviousOutPoint: OutPoint{
				Tree: output.Tree,
			},
			ValueIn: output.Amount.Copy(),
		}
		tx.TxIn = append(tx.TxIn, txIn)

		txSize := TxSerializeSize(tx) + spendSize*len(tx.TxIn)

		// Calculate the fee required for the txn at this point
		// observing the specified fee rate. If we don't have enough
		// coins from he current amount selected to pay the fee, then
		// continue to grab more coins.
		reqFee := coin.Amount{int64(txSize) * feeRate.AtomsValue}
		collected := amtSelected.AtomsValue - reqFee.AtomsValue
		if collected < amt.AtomsValue {
			continue
		}

		// If we have any change left over, then add an additional
		// output to the transaction reserved for change.
		changeVal := coin.Amount{amtSelected.AtomsValue - amt.AtomsValue - reqFee.AtomsValue}
		if changeVal.AtomsValue > 0 {
			addr, err := wallet.GetNewAddress(account)
			if err != nil {
				return err
			}
			pkScript, err := PayToAddrScript(addr)
			if err != nil {
				return err
			}
			changeOutput := &TxOut{
				Value:    changeVal,
				PkScript: pkScript,
			}
			tx.TxOut = append(tx.TxOut, changeOutput)
		}
		return nil
	}

	// If we've reached this point, then coin selection failed due to an
	// insufficient amount of coins.
	return fmt.Errorf("not enough funds for coin selection")
}
