package consolewallet

import (
	"fmt"
	"github.com/jfixby/coinharness"
	"github.com/jfixby/pin"
	"github.com/jfixby/pin/commandline"
	"net"
	"path/filepath"
	"strconv"
)

type NewConsoleWalletArgs struct {
	ClientFac                    coinharness.RPCClientFactory
	ConsoleCommandCook           ConsoleCommandCook
	WalletExecutablePathProvider commandline.ExecutablePathProvider

	AppDir    string
	ActiveNet coinharness.Network

	NodeRPCHost string
	NodeRPCPort int
	NodeUser    string
	NodePass    string

	WalletRPCHost string
	WalletRPCPort int
	WalletUser    string
	WalletPass    string
}

func NewConsoleWallet(args *NewConsoleWalletArgs) *ConsoleWallet {
	pin.AssertNotNil("WalletExecutablePathProvider", args.WalletExecutablePathProvider)
	pin.AssertNotNil("ActiveNet", args.ActiveNet)
	pin.AssertNotNil("ClientFac", args.ClientFac)

	pin.AssertNotNil("args.NodeRPCHost", args.NodeRPCHost)
	pin.AssertNotEmpty("args.NodeRPCHost", args.NodeRPCHost)

	pin.AssertNotNil("args.WalletRPCHost", args.WalletRPCHost)
	pin.AssertNotEmpty("args.WalletRPCHost", args.WalletRPCHost)

	pin.AssertTrue("args.NodeRPCPort", args.NodeRPCPort != 0)
	pin.AssertTrue("args.WalletRPCPort", args.WalletRPCPort != 0)

	Wallet := &ConsoleWallet{
		nodeRPCListener:              net.JoinHostPort(args.NodeRPCHost, strconv.Itoa(args.NodeRPCPort)),
		walletRpcListener:            net.JoinHostPort(args.WalletRPCHost, strconv.Itoa(args.WalletRPCPort)),
		WalletRpcUser:                args.WalletUser,
		WalletRpcPass:                args.WalletPass,
		NodeRpcUser:                  args.NodeUser,
		NodeRpcPass:                  args.NodePass,
		appDir:                       args.AppDir,
		endpoint:                     "ws",
		rPCClient:                    &coinharness.RPCConnection{MaxConnRetries: 20, RPCClientFactory: args.ClientFac},
		WalletExecutablePathProvider: args.WalletExecutablePathProvider,
		network:                      args.ActiveNet,
		ConsoleCommandCook:           args.ConsoleCommandCook,
	}
	return Wallet
}

// ConsoleWallet launches a new dcrd instance using command-line call.
// Implements harness.Testwallet.
type ConsoleWallet struct {
	// WalletExecutablePathProvider returns path to the dcrd executable
	WalletExecutablePathProvider commandline.ExecutablePathProvider

	NodeRpcUser string
	NodeRpcPass string

	WalletRpcUser string
	WalletRpcPass string

	nodeRPCListener   string
	walletRpcListener string
	appDir            string
	debugLevel        string
	endpoint          string

	externalProcess commandline.ExternalProcess

	rPCClient *coinharness.RPCConnection

	network coinharness.Network

	ConsoleCommandCook ConsoleCommandCook
}

type ConsoleCommandParams struct {
	ExtraArguments map[string]interface{}
	NodeRpcUser    string
	NodeRpcPass    string
	WalletRpcUser  string
	WalletRpcPass  string
	RpcConnect     string
	RpcListen      string
	AppDir         string
	DebugLevel     string
	CertFile       string
	NodeCertFile   string
	KeyFile        string
	Network        coinharness.Network
}

type ConsoleCommandCook interface {
	CookArguments(par *ConsoleCommandParams) map[string]interface{}
}

// RPCConnectionConfig produces a new connection config instance for RPC client
func (wallet *ConsoleWallet) RPCConnectionConfig() coinharness.RPCConnectionConfig {
	return coinharness.RPCConnectionConfig{
		Host:            wallet.walletRpcListener,
		Endpoint:        wallet.endpoint,
		User:            wallet.WalletRpcUser,
		Pass:            wallet.WalletRpcPass,
		CertificateFile: wallet.CertFile(),
	}
}

// FullConsoleCommand returns the full console command used to
// launch external process of the Wallet
func (wallet *ConsoleWallet) FullConsoleCommand() string {
	return wallet.externalProcess.FullConsoleCommand()
}

// RPCClient returns Wallet RPCConnection
func (wallet *ConsoleWallet) RPCClient() *coinharness.RPCConnection {
	return wallet.rPCClient
}

// CertFile returns file path of the .cert-file of the Wallet
func (wallet *ConsoleWallet) CertFile() string {
	return filepath.Join(wallet.appDir, "rpc.cert")
}

// KeyFile returns file path of the .key-file of the Wallet
func (wallet *ConsoleWallet) KeyFile() string {
	return filepath.Join(wallet.appDir, "rpc.key")
}

// Network returns current network of the Wallet
func (wallet *ConsoleWallet) Network() coinharness.Network {
	return wallet.network
}

// IsRunning returns true if ConsoleWallet is running external dcrd process
func (wallet *ConsoleWallet) IsRunning() bool {
	return wallet.externalProcess.IsRunning()
}

// Start Wallet process. Deploys working dir, launches dcrd using command-line,
// connects RPC client to the wallet.
func (wallet *ConsoleWallet) Start(args *coinharness.TestWalletStartArgs) error {
	if wallet.IsRunning() {
		pin.ReportTestSetupMalfunction(fmt.Errorf("ConsoleWallet is already running"))
	}
	fmt.Println("Start Wallet process...")
	pin.MakeDirs(wallet.appDir)

	exec := wallet.WalletExecutablePathProvider.Executable()
	wallet.externalProcess.CommandName = exec

	consoleCommandParams := &ConsoleCommandParams{
		ExtraArguments: args.ExtraArguments,
		NodeRpcUser:    wallet.NodeRpcUser,
		NodeRpcPass:    wallet.NodeRpcPass,
		WalletRpcUser:  wallet.WalletRpcUser,
		WalletRpcPass:  wallet.WalletRpcPass,
		RpcConnect:     wallet.nodeRPCListener,
		RpcListen:      wallet.walletRpcListener,
		AppDir:         wallet.appDir,
		DebugLevel:     wallet.debugLevel,
		CertFile:       wallet.CertFile(),
		NodeCertFile:   args.NodeRPCCertFile,
		KeyFile:        wallet.KeyFile(),
		Network:        wallet.network,
	}

	wallet.externalProcess.Arguments = commandline.ArgumentsToStringArray(
		wallet.ConsoleCommandCook.CookArguments(consoleCommandParams),
	)
	wallet.externalProcess.Launch(args.DebugOutput)
	// Wallet RPC instance will create a cert file when it is ready for incoming calls
	pin.WaitForFile(wallet.CertFile(), 15)

	fmt.Println("Connect to Wallet RPC...")
	cfg := wallet.RPCConnectionConfig()
	wallet.rPCClient.Connect(cfg, nil)
	fmt.Println("Wallet RPC client connected.")

	return nil
}

// Stop interrupts the running Wallet process.
// Disconnects RPC client from the Wallet, removes cert-files produced by the dcrd,
// stops dcrd process.
func (wallet *ConsoleWallet) Stop() {
	if !wallet.IsRunning() {
		pin.ReportTestSetupMalfunction(fmt.Errorf("Wallet is not running"))
	}

	if wallet.rPCClient.IsConnected() {
		fmt.Println("Disconnect from Wallet RPC...")
		wallet.rPCClient.Disconnect()
	}

	fmt.Println("Stop Wallet process...")
	err := wallet.externalProcess.Stop()
	pin.CheckTestSetupMalfunction(err)

	// Delete files, RPC servers will recreate them on the next launch sequence
	pin.DeleteFile(wallet.CertFile())
	pin.DeleteFile(wallet.KeyFile())

}

// Dispose simply stops the Wallet process if running
func (wallet *ConsoleWallet) Dispose() error {
	if wallet.IsRunning() {
		wallet.Stop()
	}
	return nil
}

func (wallet *ConsoleWallet) NewAddress(arg *coinharness.NewAddressArgs) (coinharness.Address, error) {
	if arg == nil {
		arg = &coinharness.NewAddressArgs{}
		arg.Account = "default"
	}
	return wallet. //
		RPCClient(). //
		Connection(). //
		GetNewAddress(arg.Account)
}

func (wallet *ConsoleWallet) SendOutputs(args *coinharness.SendOutputsArgs) (coinharness.Hash, error) {
	panic("")
}

func (wallet *ConsoleWallet) Sync(desiredHeight int64) int64 {
	for wallet.SyncedHeight() < desiredHeight {
		pin.Sleep(1000)
		count := wallet.SyncedHeight()
		fmt.Println("   sync to: " + strconv.FormatInt(count, 10))
	}
	return wallet.SyncedHeight()
}

func (wallet *ConsoleWallet) SyncedHeight() int64 {
	rpcClient := wallet.rPCClient.Connection()
	_, h, err := rpcClient.GetBestBlock()
	pin.CheckTestSetupMalfunction(err)
	return h
}

func (wallet *ConsoleWallet) UnlockOutputs(inputs []coinharness.InputTx) {
	wallet.rPCClient.Connection().UnlockOutputs(inputs)
}

func (wallet *ConsoleWallet) CreateNewAccount(accountName string) error {
	return wallet.rPCClient.Connection().CreateNewAccount(accountName)
}

func (wallet *ConsoleWallet) GetBalance(accountName string) (coinharness.CoinsAmount, error) {
	return wallet.rPCClient.Connection().GetBalance(accountName)
}

func (wallet *ConsoleWallet) GetNewAddress(accountName string) (coinharness.Address, error) {
	return wallet.rPCClient.Connection().GetNewAddress(accountName)
}

func (wallet *ConsoleWallet) ValidateAddress(address coinharness.Address) (*coinharness.ValidateAddressResult, error) {
	return wallet.rPCClient.Connection().ValidateAddress(address)
}

func (wallet *ConsoleWallet) ConfirmedBalance() coinharness.CoinsAmount {
	return wallet.rPCClient.Connection().ConfirmedBalance()
}

func (wallet *ConsoleWallet) CreateTransaction(args *coinharness.CreateTransactionArgs) (coinharness.CreatedTransactionTx, error) {
	return wallet.rPCClient.Connection().CreateTransaction(args)
}
