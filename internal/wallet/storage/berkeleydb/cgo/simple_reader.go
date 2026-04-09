// +build cgo

package cgo

/*
#cgo darwin,arm64 CFLAGS: -I/opt/homebrew/opt/berkeley-db@4/include
#cgo darwin,arm64 LDFLAGS: -L/opt/homebrew/opt/berkeley-db@4/lib -ldb
#cgo darwin,amd64 CFLAGS: -I/usr/local/opt/berkeley-db@4/include
#cgo darwin,amd64 LDFLAGS: -L/usr/local/opt/berkeley-db@4/lib -ldb
#cgo linux CFLAGS: -I/usr/include
#cgo linux LDFLAGS: -ldb

#include <stdlib.h>
#include <string.h>
#include <db.h>

// Entry result structure
typedef struct {
    void *key_data;
    size_t key_size;
    void *val_data;
    size_t val_size;
    int ret;
} entry_result;

// Simple wrapper functions declared as extern to avoid duplicate symbols
extern int bdb_wrapper_open(DB **dbp, const char *file);
extern int bdb_wrapper_close(DB *dbp);
extern int bdb_create_cursor(DB *dbp, DBC **cursor);
extern int bdb_close_cursor(DBC *cursor);
extern int bdb_cursor_next(DBC *cursor, entry_result *result);
*/
import "C"
import (
	"fmt"
	"os"
	"runtime"
	"unsafe"
)

// Entry represents a key-value pair from BerkeleyDB
type Entry struct {
	Key   []byte
	Value []byte
}

// SimpleReader provides BerkeleyDB access through CGo bindings
type SimpleReader struct {
	db       *C.DB
	filename string
}

// OpenSimple opens a BerkeleyDB file using CGo bindings
func OpenSimple(filename string) (*SimpleReader, error) {
	// Initialize embedded library if not already loaded.
	// On Windows, BDB is statically linked via CGO — no embedded shared library needed.
	_, err := InitEmbeddedLibrary()
	if err != nil && runtime.GOOS != "windows" {
		return nil, fmt.Errorf("failed to initialize embedded BerkeleyDB library: %w", err)
	}

	// Check if file exists
	if _, err := os.Stat(filename); err != nil {
		return nil, fmt.Errorf("wallet file not found: %s", filename)
	}

	reader := &SimpleReader{
		filename: filename,
	}

	// Convert filename to C string
	cFilename := C.CString(filename)
	defer C.free(unsafe.Pointer(cFilename))

	// Open the database
	ret := C.bdb_wrapper_open(&reader.db, cFilename)
	if ret != 0 {
		return nil, fmt.Errorf("failed to open BerkeleyDB: error code %d", ret)
	}

	return reader, nil
}

// Close closes the BerkeleyDB database
func (r *SimpleReader) Close() error {
	if r.db != nil {
		ret := C.bdb_wrapper_close(r.db)
		if ret != 0 {
			return fmt.Errorf("failed to close database: %d", ret)
		}
		r.db = nil
	}
	return nil
}

// GetAllEntries reads all entries from the BerkeleyDB database using cursor
func (r *SimpleReader) GetAllEntries() ([]Entry, error) {
	var entries []Entry

	// Create a cursor
	var cursor *C.DBC
	ret := C.bdb_create_cursor(r.db, &cursor)
	if ret != 0 {
		return nil, fmt.Errorf("failed to create cursor: %d", ret)
	}
	defer C.bdb_close_cursor(cursor)

	// Iterate through all entries
	for {
		var result C.entry_result
		ret = C.bdb_cursor_next(cursor, &result)

		if ret == C.DB_NOTFOUND {
			break // No more entries
		}
		if ret != 0 {
			return nil, fmt.Errorf("cursor get failed: %d", ret)
		}

		// Copy key and value data to Go slices
		keyBytes := C.GoBytes(result.key_data, C.int(result.key_size))
		valueBytes := C.GoBytes(result.val_data, C.int(result.val_size))

		entries = append(entries, Entry{
			Key:   keyBytes,
			Value: valueBytes,
		})
	}

	return entries, nil
}