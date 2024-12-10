package plotthread

import (
	"bytes"
	"encoding/binary"
	"encoding/gob"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"

	"github.com/buger/jsonparser"
	"github.com/pierrec/lz4"
	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/opt"
)

// PlotStorageDisk is an on-disk PlotStorage implementation using the filesystem for plots
// and LevelDB for plot headers.
type PlotStorageDisk struct {
	db       *leveldb.DB
	dirPath  string
	readOnly bool
	compress bool
}

// NewPlotStorageDisk returns a new instance of on-disk plot storage.
func NewPlotStorageDisk(dirPath, dbPath string, readOnly, compress bool) (*PlotStorageDisk, error) {
	// create the plots path if it doesn't exist
	if !readOnly {
		if info, err := os.Stat(dirPath); os.IsNotExist(err) {
			if err := os.MkdirAll(dirPath, 0700); err != nil {
				return nil, err
			}
		} else if !info.IsDir() {
			return nil, fmt.Errorf("%s is not a directory", dirPath)
		}
	}

	// open the database
	opts := opt.Options{ReadOnly: readOnly}
	db, err := leveldb.OpenFile(dbPath, &opts)
	if err != nil {
		return nil, err
	}
	return &PlotStorageDisk{
		db:       db,
		dirPath:  dirPath,
		readOnly: readOnly,
		compress: compress,
	}, nil
}

// Store is called to store all of the plot's information.
func (b PlotStorageDisk) Store(id PlotID, plot *Plot, now int64) error {
	if b.readOnly {
		return fmt.Errorf("Plot storage is in read-only mode")
	}

	// save the complete plot to the filesystem
	plotBytes, err := json.Marshal(plot)
	if err != nil {
		return err
	}

	var ext string
	if b.compress {
		// compress with lz4
		in := bytes.NewReader(plotBytes)
		zout := new(bytes.Buffer)
		zw := lz4.NewWriter(zout)
		if _, err := io.Copy(zw, in); err != nil {
			return err
		}
		if err := zw.Close(); err != nil {
			return err
		}
		plotBytes = zout.Bytes()
		ext = ".lz4"
	} else {
		ext = ".json"
	}

	// write the plot and sync
	plotPath := filepath.Join(b.dirPath, id.String()+ext)
	f, err := os.OpenFile(plotPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	n, err := f.Write(plotBytes)
	if err != nil {
		return err
	}
	if err == nil && n < len(plotBytes) {
		return io.ErrShortWrite
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}

	// save the header to leveldb
	encodedPlotHeader, err := encodePlotHeader(plot.Header, now)
	if err != nil {
		return err
	}

	wo := opt.WriteOptions{Sync: true}
	return b.db.Put(id[:], encodedPlotHeader, &wo)
}

// Get returns the referenced plot.
func (b PlotStorageDisk) GetPlot(id PlotID) (*Plot, error) {
	plotJson, err := b.GetPlotBytes(id)
	if err != nil {
		return nil, err
	}

	// unmarshal
	plot := new(Plot)
	if err := json.Unmarshal(plotJson, plot); err != nil {
		return nil, err
	}
	return plot, nil
}

// GetPlotBytes returns the referenced plot as a byte slice.
func (b PlotStorageDisk) GetPlotBytes(id PlotID) ([]byte, error) {
	var ext [2]string
	if b.compress {
		// order to try finding the plot by extension
		ext = [2]string{".lz4", ".json"}
	} else {
		ext = [2]string{".json", ".lz4"}
	}

	var compressed bool = b.compress

	plotPath := filepath.Join(b.dirPath, id.String()+ext[0])
	if _, err := os.Stat(plotPath); os.IsNotExist(err) {
		compressed = !compressed
		plotPath = filepath.Join(b.dirPath, id.String()+ext[1])
		if _, err := os.Stat(plotPath); os.IsNotExist(err) {
			// not found
			return nil, nil
		}
	}

	// read it off disk
	plotBytes, err := ioutil.ReadFile(plotPath)
	if err != nil {
		return nil, err
	}

	if compressed {
		// uncompress
		zin := bytes.NewBuffer(plotBytes)
		out := new(bytes.Buffer)
		zr := lz4.NewReader(zin)
		if _, err := io.Copy(out, zr); err != nil {
			return nil, err
		}
		plotBytes = out.Bytes()
	}

	return plotBytes, nil
}

// GetPlotHeader returns the referenced plot's header and the timestamp of when it was stored.
func (b PlotStorageDisk) GetPlotHeader(id PlotID) (*PlotHeader, int64, error) {
	// fetch it
	encodedHeader, err := b.db.Get(id[:], nil)
	if err == leveldb.ErrNotFound {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}

	// decode it
	return decodePlotHeader(encodedHeader)
}

// GetRepresentation returns a representation within a plot and the plot's header.
func (b PlotStorageDisk) GetRepresentation(id PlotID, index int) (
	*Representation, *PlotHeader, error) {
	plotJson, err := b.GetPlotBytes(id)
	if err != nil {
		return nil, nil, err
	}

	// pick out and unmarshal the representation at the index
	idx := "[" + strconv.Itoa(index) + "]"
	txJson, _, _, err := jsonparser.Get(plotJson, "representations", idx)
	if err != nil {
		return nil, nil, err
	}
	tx := new(Representation)
	if err := json.Unmarshal(txJson, tx); err != nil {
		return nil, nil, err
	}

	// pick out and unmarshal the header
	hdrJson, _, _, err := jsonparser.Get(plotJson, "header")
	if err != nil {
		return nil, nil, err
	}
	header := new(PlotHeader)
	if err := json.Unmarshal(hdrJson, header); err != nil {
		return nil, nil, err
	}
	return tx, header, nil
}

// Close is called to close any underlying storage.
func (b *PlotStorageDisk) Close() error {
	return b.db.Close()
}

// leveldb schema: {bid} -> {timestamp}{gob encoded header}

func encodePlotHeader(header *PlotHeader, when int64) ([]byte, error) {
	buf := new(bytes.Buffer)
	if err := binary.Write(buf, binary.BigEndian, when); err != nil {
		return nil, err
	}
	enc := gob.NewEncoder(buf)
	if err := enc.Encode(header); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func decodePlotHeader(encodedHeader []byte) (*PlotHeader, int64, error) {
	buf := bytes.NewBuffer(encodedHeader)
	var when int64
	if err := binary.Read(buf, binary.BigEndian, &when); err != nil {
		return nil, 0, err
	}
	enc := gob.NewDecoder(buf)
	header := new(PlotHeader)
	if err := enc.Decode(header); err != nil {
		return nil, 0, err
	}
	return header, when, nil
}
