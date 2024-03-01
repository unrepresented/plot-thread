# inspector

The inspector is a simple tool for examining the offline plot thread data

## To install

1. Make sure you have the new Go modules support enabled: `export GO111MODULE=on`
2. `go install github.com/unrepresented/plot-thread/inspector`

The `inspector` application is now in `$HOME/go/bin/inspector`.

## Basic command line arguments

`inspector -datadir <plot thread data directory> -command <command> [other flags required per command]`

## Commands

* **height** - Display the current plot thread height.
* **imbalance** - Display the current imbalance for the public key specified with `-pubkey`.
* **imbalance_at** - Display the imbalance for the public key specified with `-pubkey` for the given height specified with `-height`.
* **plot** - Display the plot specified with `-plot_id`.
* **plot_at** - Display the plot at the plot thread height specified with `-height`.
* **tx** - Display the representation specified with `-tx_id`.
* **history** - Display representation history for the public key specified with `-pubkey`. Other options for this command include `-start_height`, `-end_height`, `-start_index`, and `-limit`.
* **verify** - Verify the sum of all public key imbalances matches what's expected dictated by the plot reward schedule. If `-pubkey` is specified, it verifies the public key's imbalance matches the imbalance computed using the public key's representation history.
