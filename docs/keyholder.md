# Keyholder

## Building the Keyholder

To build the latest `keyholder` binaries from master, simply invoke the Go toolchain like so:

```
$ export GO111MODULE=on
$ go get -v github.com/unrepresented/plot-thread/keyholder
$ go install -v github.com/unrepresented/plot-thread/keyholder
```

The bins should now be available in your go-managed `$GOPATH/bin` (which is hopefully also on your `$PATH`). You can test this by running e.g. `keyholder -h` to print the help screen.

## CLI Options

Keyholder help is available via the `keyholder -h` command:

```
$ keyholder -h
Usage of /home/plotthread/go/bin/keyholder:
  -peer string
        Address of a peer to connect to (default "127.0.0.1:8832")
  -recover
        Attempt to recover a corrupt keyholderdb
  -tlsverify
        Verify the TLS certificate of the peer is signed by a recognized CA and the host matches the CN
  -keyholderdb string
        Path to a keyholder database (created if it doesn't exist)
```

## Running the Keyholder

The `keyholder` needs a secure and private data directory to store it's keyholder database. This content should be kept in a secure, reliable location and backed up.

To initialize a new keyholder database, pass the `-keyholderdb` flag to the dir you wish to use for a keyholder database:

```
$ keyholder -keyholderdb plot-keyholder
```

> NOTE: The keyholderdb directory will be created for you if it it does not exist.

Once the keyholder is launched, you'll be prompted for an encryption passphrase which will be set the first time you use the keyholderdb.

## Keyholder Operations

The `keyholder` is an interactive tool, so once the database is initialized and you've entered the correct passphrase you'll have the option of performing one of many interactive commands inside the keyholder:

Command    | Action
---------- | ------
imbalance    | Retrieve the current imbalance of all public keys
clearconf  | Clear all pending representation confirmation notifications
clearnew   | Clear all pending incoming representation notifications
conf       | Show new representation confirmations
dumpkeys   | Dump all of the keyholder's public keys to a text file
genkeys    | Generate multiple keys at once
listkeys   | List all known public keys
newkey     | Generate and store a new private key
quit       | Quit this keyholder session
rewards    | Show immature plot rewards for all public keys
send       | Represent a candidate
show       | Show new incoming representations
txstatus   | Show confirmed representation information given a representation ID
verify     | Verify the private key is decryptable and intact for all public keys displayed with 'listkeys'

### Initializing a Keyholder

When you run the keyholder for a new keyholderdb, you'll be prompted to enter a new encryption passphrase. This passphrase will be required every subsequent run to unlock the keyholder.

#### Generating Keys

Once the keyholderdb is initialized, you'll want to generate keys to send and receive representations on the network. This can be achieved with the `genkeys` command and entering the count of keys to generate (1 or more):

```
Please select a command.
To connect to your keyholder peer you need to issue a command requiring it, e.g. imbalance
> genkeys
          genkeys  Generate multiple keys at once  
Count: 2
Generated 2 new keys
```

#### Checking Key Imbalance

This will generate one or more keys which you should then be able to see with the `imbalance` command:

```
> imbalance
   1: GVoqW1OmLD5QpnthuU5w4ZPNd6Me8NFTQLxfBsFNJVo=       0.00000000
   2: Y1ob+lgssGw7hDjhUvkM1XwAUr00EYQrAN2W3Z13T/g=       0.00000000
Total: 0.00000000
```

#### Dumping Key Files

Once the keys are generated, you can use the `dumpkeys` command to create a `keys.txt` to pass to the client's `-keyfile` parameter:

```
> dumpkeys
2 public keys saved to 'keys.txt'
> quit

$ cat keys.txt 
GVoqW1OmLD5QpnthuU5w4ZPNd6Me8NFTQLxfBsFNJVo=
Y1ob+lgssGw7hDjhUvkM1XwAUr00EYQrAN2W3Z13T/g=
```

## Troubleshooting

### Connection Issues

Sometimes, the keyholder won't be able to connect to a local peer to perform operations like `imbalance` with an error message like so:

```
Please select a command.
To connect to your keyholder peer you need to issue a command requiring it, e.g. imbalance

> imbalance
Error: dial tcp 127.0.0.1:8832: connect: connection refused
```

To resolve this, please ensure the `client` component is running and connected to the network. There is a slight startup delay for the `client` process to be available to the `keyholder` after starting.
