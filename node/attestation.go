package node

import (
	context "context"
	json "encoding/json"
	"strconv"
	"time"

	"github.com/Lagrange-Labs/Lagrange-Node/bcclients"
	"github.com/Lagrange-Labs/Lagrange-Node/network"
	"github.com/Lagrange-Labs/Lagrange-Node/utils"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	ethClient "github.com/ethereum/go-ethereum/ethclient"
)

// Format of gossiped state root message
type StateRootMessage struct {
	StateRoot                  string
	Timestamp                  string
	BlockNumber                string
	ShardedEdDSASignatureTuple string
	ECDSASignatureTuple        string
}

func GenerateStateRootString(eth *ethClient.Client, block *types.Block) string {
	//5. ECDSA Signature Tuple (Parameters V,R,S): This signature should be done on a hash of the State root, Timestamp, Block Number and Sharded EdDSA Signature Tuple
	stateRootSeparator := utils.GetSeparator()

	blockRoot := block.Root().String()
	blockTime := strconv.FormatUint(block.Time(), 10)
	blockNumber := block.Number().String()
	chain, err := eth.ChainID(context.Background())
	if err != nil {
		panic(err)
	}
	chainID := chain.String()
	salt := utils.GenSalt32()

	stateRootStr := blockRoot + stateRootSeparator + blockTime + stateRootSeparator + blockNumber + stateRootSeparator + chainID + stateRootSeparator + salt
	utils.LogMessage("State Root String: "+stateRootStr, utils.LOG_INFO)

	return stateRootStr
}

// Simple pop function for discarding offline/malfunctioning endpoints
func ethClientsShift(ethClients []*ethClient.Client, recycle bool) (*ethClient.Client, []*ethClient.Client) {
	eth := ethClients[0]
	ethClients = ethClients[1:]
	if recycle {
		ethClients = append(ethClients, eth)
	}
	return eth, ethClients
}

/*
For gossiping of state roots:

1. State Root
2. Timestamp
3. Block Number
4. Sharded EdDSA Signature Tuple (TBD exact parameters)
5. ECDSA Signature Tuple (Parameters V,R,S): This signature should be done on a hash of the State root, Timestamp, Block Number and Sharded EdDSA Signature Tuple
6. Ethereum Public Key
*/
func (lnode *LagrangeNode) ListenForBlocks() {
	ethClients := lnode.ethAttestClients
	node := lnode.node
	topic := lnode.topic

	// Separator for gossip messaging
	stateRootSeparator := utils.GetSeparator()
	// Pull ethClient from list of clients
	eth, ethClients := ethClientsShift(ethClients, true)
	// Track failures of cycled RPC endpoints in order to panic if all fail.
	clientFailures := 0
	for {
		block, err := eth.BlockByNumber(context.Background(), nil)

		// If RPC request fails, use the next one in the list until there are none left.  Then panic.
		if err != nil {
			clientFailures++
			if len(ethClients) > 1 && clientFailures < len(ethClients) {
				eth, ethClients = ethClientsShift(ethClients, true)
			} else {
				panic(err)
			}
		} else {
			clientFailures = 0
		}

		// concatenate relevant fields
		stateRootStr := GenerateStateRootString(eth, block)

		//ShardedEdDSASignatureTuple - TBD
		shardedSignatureTuple := ""

		stateRootStrWithShardedSignatureTuple := stateRootStr + stateRootSeparator + shardedSignatureTuple

		// generate hash from concatenated fields
		stateHash := bcclients.KeccakHash(stateRootStrWithShardedSignatureTuple)

		// sign resultant hash
		signature, err := lnode.keystore.SignHash(lnode.account, []byte(stateHash))
		if err != nil {
			panic(err)
		}
		ecdsaSignatureHex := hexutil.Encode(signature)

		//timestamp
		timestamp := time.Now().UTC().Unix()

		stateRootMessage := StateRootMessage{
			StateRoot:                  stateRootStr,
			Timestamp:                  strconv.FormatInt(timestamp, 10),
			BlockNumber:                block.Number().String(),
			ShardedEdDSASignatureTuple: shardedSignatureTuple,
			ECDSASignatureTuple:        ecdsaSignatureHex}

		json, err := json.Marshal(stateRootMessage)
		if err != nil {
			panic(err)
		}
		bytes := []byte(json)
		msg := string(bytes)

		if err = network.WriteMessages(node, topic, lnode.GetAddressString(), msg, "StateRootMessage"); err != nil {
			panic(err)
		}

		time.Sleep(1 * time.Second)
	}
}