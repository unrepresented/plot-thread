# Client

## Building the Client

To build the latest keyholder binaries from master, simply invoke the Go toolchain like so:

```
$ export GO111MODULE=on
$ go get -v github.com/unrepresented/plot-thread/client
$ go install -v github.com/unrepresented/plot-thread/client
```

The plotthread bins should now be available in your go-managed `$GOPATH/bin` (which is hopefully also on your `$PATH`). You can test this by running `client -h` to print the help screen.

## CLI Options

Client help is available via the `client -h` command:

```
$ client -h
Usage of /home/plotthread/go/bin/client:
  -compress
        Compress plots on disk with lz4
  -datadir string
        Path to a directory to save plot thread data
  -dnsseed
        Run a DNS server to allow others to find peers
  -inlimit int
        Limit for the number of inbound peer connections. (default 128)
  -keyfile string
        Path to a file containing public keys to use when scribing
  -memo string
        A memo to include in newly scribed plots
  -noaccept
        Disable inbound peer connections
  -noirc
        Disable use of IRC for peer discovery
  -numscribers int
        Number of scribers to run (default 1)
  -peer string
        Address of a peer to connect to
  -port int
        Port to listen for incoming peer connections (default 8832)
  -prune
        Prune representation and public key representation indices
  -pubkey string
        A public key which receives newly scribed plot rewards
  -tlscert string
        Path to a file containing a PEM-encoded X.509 certificate to use with TLS
  -tlskey string
        Path to a file containing a PEM-encoded private key to use with TLS
  -upnp
        Attempt to forward the plotthread port on your router with UPnP
```

## Running the Client

The client requires a data dir for storage of the plot-thread and general metadata as well as one or more public keys to send plot rewards to upon scribing. Otherwise, running the client is as simple as:

```
$ client -datadir plot-data -keyfile keys.txt -numscribers 2
```

### Configuring Peer Discovery

The client supports two modes of peer discovery: DNS with IRC as fallback.

If you want to run a DNS server to help enable peer discovery, you can pass the `-dnsseed` flag.

If you wish to disable IRC discovery, that can be disabled via the `-noirc` flag.

If you wish to enable UPnP port forwarding for the client node, use the `-upnp` flag.

### Configuring Scribers

In order to effectively scribe, you'll typically want to run one scriber per CPU core on your machine. This is configured via the `-numscribers` param, like so:

```
$ client ... -numscribers 4
```

To run a scriber-less node, you can pass `0` as the number of scribers like so:

```
$ client ... -numscribers 0
```

### Configuring Keys

The client supports two modes of plot reward representations for scribing: single key and key list targets.

To distribute plot rewards to a single key, use the `-pubkey` flag to pass the target public key in the CLI command.

To distribute plot rewards to multiple keys, use the `-keyfile` flag with a text file of the public keys (one per line).

> NOTE: The keyholder components `dumpkeys` command will generate a `keys.txt` for you as part of keyholder setup.

## Terminating the client

The client runs synchronously in the current window, so to exit simply hit control-c for a graceful shutdown.
