package main

import "fmt"
import "flag"
import "os"
import "os/signal"
import "syscall"
import "context"
//import "strconv"

import host "github.com/libp2p/go-libp2p-core/host"
import ping "github.com/libp2p/go-libp2p/p2p/protocol/ping"

//import ethClient "github.com/ethereum/go-ethereum/ethclient"

func main() {
	// Parse Port
	portPtr := flag.Int("port",8081,"Server listening port")
	// Parse Nickname
	nickPtr := flag.String("nick","","Nickname - CLI flag, blank by default, consider addresses or protocol TLDs later.")
	// Parse Room
	roomPtr := flag.String("room","rinkeby","Room / Network")
	// Parse Remote Peer
	peerAddrPtr := flag.String("peerAddr","","Remote Peer Address")
	// Parse ETH URL
	ethEndpointPtr := flag.String("ethEndpoint","https://eth-mainnet.gateway.pokt.network/v1/5f3453978e354ab992c4da79","Ethereum Endpoint URL:Port")

	flag.Parse()

	port := *portPtr
	nick := *nickPtr
	room := *roomPtr
	peerAddr := *peerAddrPtr
	ethEndpoint := *ethEndpointPtr
	
	fmt.Println("Port:",port)

	/*
	rpc := loadRpcClient(ethEndpoint)
	rpcCall(rpc,"0xcc13fc627effd6e35d2d2706ea3c4d7396c610ea","0x8da5cb5b")
	*/
	eth := loadEthClient(ethEndpoint)
	
	// Create listener
	node := createListener(port)

	if(len(nick) == 0) {
		nick = fmt.Sprintf("%s-%s", os.Getenv("USER"), shortID(node.ID()))
	}
	fmt.Println("Nickname:",nick)

	// Get P2P Address Info
	localInfo := getAddrInfo(node);
	_ = localInfo

	// Ping test - please determine an approach to finding peers, rather than self-pinging	
	ch := ping.Ping(context.Background(), node, localInfo.ID)
	for i := 0; i < 5; i++ {
		res := <-ch
		fmt.Println("Got ping response.", "Latency:", res.RTT)
	}
	
	// Connect to Remote Peer
	connectRemote(node,peerAddr)
	
	ps, topic, subscription := getGossipSub(node,room)

	go handleMessaging(node,topic,ps,nick,subscription)
	go listenForBlocks(eth,node,topic,ps,nick,subscription)

        // SIGINT | SIGTERM Signal Handling - End
        termHandler(node)
}

func termHandler(node host.Host) {
        ch := make(chan os.Signal, 1)
        signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
        <-ch
        fmt.Println("Received signal, shutting down...")

        // shut the node down
        if err := node.Close(); err != nil {
                panic(err)
        }
}
