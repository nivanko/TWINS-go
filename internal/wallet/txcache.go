package wallet

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/twins-dev/twins-core/pkg/types"
)

const (
	txCacheMagic   = "TWINSTxCache"
	txCacheVersion uint32 = 3 // bumped: removed Staked field from Balance serialization

	// Deserialization safety limits (DoS prevention for malicious cache files)
	maxScriptSize    = 10000     // Maximum script size in bytes
	maxVarStringSize = 1000000   // 1MB limit for variable-length strings
	maxTxCount       = 10000000  // 10M transactions upper bound
	maxUTXOCount     = 10000000  // 10M UTXOs upper bound
	maxBalanceCount  = 10000000  // 10M balance entries upper bound

	// checksumSize is the trailing SHA256 hash size
	checksumSize = sha256.Size // 32 bytes
)

// Transaction flag bits (serialized as single byte)
const (
	txFlagWatchOnly    byte = 0x01
	txFlagIsConflicted byte = 0x02
	// txFlagIsSynthetic marks entries stored under vout=1 (secondary map key).
	// Used for the staking-reward portion of a combined staker+MN coinstake tx,
	// stored under txKey{hash, 1} so both rewards are visible in the GUI.
	txFlagIsSynthetic byte = 0x04
)

// txCacheEntry pairs a WalletTransaction with whether it occupies a synthetic map key.
// For normal entries isSynthetic == false and the map key equals tx.Hash.
// For secondary staking entries isSynthetic == true and the map key is txKey{tx.Hash, 1}.
type txCacheEntry struct {
	tx          *WalletTransaction
	isSynthetic bool
}

// UTXO flag bits (serialized as single byte)
const (
	utxoFlagIsCoinbase byte = 0x01
	utxoFlagIsStake    byte = 0x02
	utxoFlagIsChange   byte = 0x04
	utxoFlagSpendable  byte = 0x08
)

var (
	ErrCacheNotFound       = errors.New("txcache.dat not found")
	ErrCacheCorrupted      = errors.New("txcache.dat corrupted: checksum mismatch")
	ErrCacheInvalidMagic   = errors.New("txcache.dat invalid: wrong magic message")
	ErrCacheInvalidVersion = errors.New("txcache.dat invalid: unsupported version")
	ErrCacheStale          = errors.New("txcache.dat stale: wallet or chain state changed")
)

// LoadTransactionCache loads cached transactions, UTXOs, and balances from txcache.dat.
// Returns a non-nil error on any failure — the caller should fall back to RescanAllAddresses.
func (w *Wallet) LoadTransactionCache() error {
	cachePath := filepath.Join(w.config.DataDir, "txcache.dat")

	file, err := os.Open(cachePath)
	if err != nil {
		if os.IsNotExist(err) {
			return ErrCacheNotFound
		}
		return fmt.Errorf("failed to open txcache.dat: %w", err)
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat txcache.dat: %w", err)
	}
	if stat.Size() < checksumSize {
		return ErrCacheCorrupted
	}

	// Read payload (everything except trailing SHA256 checksum)
	dataSize := stat.Size() - checksumSize
	data := make([]byte, dataSize)
	if _, err := io.ReadFull(file, data); err != nil {
		return fmt.Errorf("failed to read txcache.dat data: %w", err)
	}

	// Verify file integrity checksum
	var storedHash [checksumSize]byte
	if _, err := io.ReadFull(file, storedHash[:]); err != nil {
		return fmt.Errorf("failed to read txcache.dat checksum: %w", err)
	}
	if sha256.Sum256(data) != storedHash {
		return ErrCacheCorrupted
	}

	r := bytes.NewReader(data)

	// 1. Magic
	magic, err := readVarString(r)
	if err != nil {
		return fmt.Errorf("failed to read magic: %w", err)
	}
	if magic != txCacheMagic {
		return ErrCacheInvalidMagic
	}

	// 2. Version
	var version uint32
	if err := binary.Read(r, binary.LittleEndian, &version); err != nil {
		return fmt.Errorf("failed to read version: %w", err)
	}
	if version != txCacheVersion {
		return ErrCacheInvalidVersion
	}

	// 3. Validation hash: SHA256(sorted addresses + bestBlockHash)
	var cachedHash [checksumSize]byte
	if _, err := io.ReadFull(r, cachedHash[:]); err != nil {
		return fmt.Errorf("failed to read validation hash: %w", err)
	}

	// Get best block hash BEFORE acquiring wallet lock (avoid deadlock with blockchain mutex)
	currentBlockHash, err := w.blockchain.GetBestBlockHash()
	if err != nil {
		return fmt.Errorf("failed to get best block hash: %w", err)
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	if w.computeAddressHashLocked(currentBlockHash) != cachedHash {
		return ErrCacheStale
	}

	// Clear existing state before loading cache (prevents merge with stale data)
	w.transactions = make(map[txKey]*WalletTransaction)
	w.utxos = make(map[types.Outpoint]*UTXO)
	w.balances = make(map[string]*Balance)
	w.nextSeqNum = 0

	// 4. Transactions
	txCount, err := readCompactSize(r)
	if err != nil {
		return fmt.Errorf("failed to read tx count: %w", err)
	}
	if txCount > maxTxCount {
		return fmt.Errorf("failed to read tx count: %d exceeds safety limit %d", txCount, maxTxCount)
	}

	var maxSeq int64
	for i := uint64(0); i < txCount; i++ {
		wtx, isSynthetic, err := deserializeWalletTx(r)
		if err != nil {
			return fmt.Errorf("failed to deserialize tx %d: %w", i, err)
		}
		// Synthetic entries (staking reward portion of a combined staker+MN coinstake)
		// are stored under vout=1 so they coexist with the MN primary entry at vout=0.
		vout := int32(0)
		if isSynthetic {
			vout = 1
		}
		w.transactions[txKey{wtx.Hash, vout}] = wtx
		if wtx.SeqNum > maxSeq {
			maxSeq = wtx.SeqNum
		}
	}
	// nextSeqNum holds the last assigned value; notifications.go increments before use
	w.nextSeqNum = maxSeq

	// 5. UTXOs
	utxoCount, err := readCompactSize(r)
	if err != nil {
		return fmt.Errorf("failed to read utxo count: %w", err)
	}
	if utxoCount > maxUTXOCount {
		return fmt.Errorf("failed to read utxo count: %d exceeds safety limit %d", utxoCount, maxUTXOCount)
	}
	for i := uint64(0); i < utxoCount; i++ {
		utxo, err := deserializeUTXO(r)
		if err != nil {
			return fmt.Errorf("failed to deserialize utxo %d: %w", i, err)
		}
		w.utxos[utxo.Outpoint] = utxo
	}

	// 6. Balances
	balCount, err := readCompactSize(r)
	if err != nil {
		return fmt.Errorf("failed to read balance count: %w", err)
	}
	if balCount > maxBalanceCount {
		return fmt.Errorf("failed to read balance count: %d exceeds safety limit %d", balCount, maxBalanceCount)
	}
	for i := uint64(0); i < balCount; i++ {
		addr, bal, err := deserializeBalance(r)
		if err != nil {
			return fmt.Errorf("failed to deserialize balance %d: %w", i, err)
		}
		w.balances[addr] = bal
	}

	// Restore the in-memory `Used` flag on every address that appears in
	// the loaded transactions, UTXOs, or balances. The flag is not
	// persisted in the cache file (nor in wallet.dat), so without this
	// pass every address would look unused after a cache-first startup,
	// breaking callers like ListAddressGroupings that filter on Used.
	markedUsed := w.markAddressesUsedFromStateLocked()

	w.logger.WithField("transactions", txCount).
		WithField("utxos", utxoCount).
		WithField("balances", balCount).
		WithField("marked_used", markedUsed).
		Info("Loaded wallet transaction cache")

	return nil
}

// markAddressesUsedFromStateLocked sets Used=true on every address that
// currently appears in the wallet transaction map, UTXO map, or balance
// map. Caller must hold w.mu (write lock). Returns the number of
// addresses newly marked as used.
//
// Two sources of truth must be kept in sync to match MarkAddressUsed
// semantics (addresses.go:314-329): the per-address `Used` field
// consulted by `ListAddressGroupings` / `GetReceivingAddresses`, and
// the `addrMgr.pool.used` map consulted by `IsAddressUsed`. Updating
// only one would leave the two diverging after a cache-first startup.
//
// The `Used` state is in-memory only (not persisted in txcache.dat or
// wallet.dat). It is normally set during `RescanAllAddresses`, block
// notifications, or `MarkAddressUsed`. Cache-first startup skips
// rescan, so this helper restores the flag from the loaded state.
func (w *Wallet) markAddressesUsedFromStateLocked() int {
	if len(w.addresses) == 0 {
		return 0
	}
	var poolUsed map[string]bool
	if w.addrMgr != nil && w.addrMgr.pool != nil {
		w.addrMgr.pool.mu.Lock()
		defer w.addrMgr.pool.mu.Unlock()
		poolUsed = w.addrMgr.pool.used
	}
	marked := 0
	mark := func(address string) {
		if address == "" {
			return
		}
		wa, ok := w.addresses[address]
		if !ok {
			return
		}
		if !wa.Used {
			wa.Used = true
			marked++
		}
		if poolUsed != nil {
			poolUsed[address] = true
		}
	}
	for _, tx := range w.transactions {
		mark(tx.Address)
	}
	for _, utxo := range w.utxos {
		mark(utxo.Address)
	}
	for addr := range w.balances {
		mark(addr)
	}
	return marked
}

// SaveTransactionCache writes transactions, UTXOs, and balances to txcache.dat.
func (w *Wallet) SaveTransactionCache() error {
	// Snapshot wallet state under write lock (SeqNums are reassigned)
	entries, utxos, balances, sortedAddrs := w.snapshotForCache()
	if sortedAddrs == nil {
		return nil // no addresses
	}

	// Get best block hash outside wallet lock (avoid deadlock with blockchain mutex)
	bestHash, err := w.blockchain.GetBestBlockHash()
	if err != nil {
		return fmt.Errorf("failed to get best block hash: %w", err)
	}

	// Compute validation hash from snapshot
	validationHash := computeAddressHash(sortedAddrs, bestHash)

	// Serialize cache data
	data, err := w.serializeCacheData(validationHash, entries, utxos, balances)
	if err != nil {
		return err
	}

	// Atomic write via tmp+rename ensures cache is never partially written (crash safety)
	cachePath := filepath.Join(w.config.DataDir, "txcache.dat")
	if err := atomicWriteFile(cachePath, data); err != nil {
		return err
	}

	w.logger.WithField("transactions", len(entries)).
		WithField("utxos", len(utxos)).
		WithField("balances", len(balances)).
		Info("Saved wallet transaction cache")

	return nil
}

// snapshotForCache takes a consistent snapshot of wallet state under write lock.
// Returns nil sortedAddrs if wallet has no addresses (skip save).
func (w *Wallet) snapshotForCache() (entries []txCacheEntry, utxos []*UTXO, balances map[string]*Balance, sortedAddrs []string) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if len(w.addresses) == 0 {
		w.logger.Debug("No addresses in wallet, skipping transaction cache save")
		return nil, nil, nil, nil
	}

	// Sort transactions by BlockHeight ASC, Time ASC for deterministic SeqNum.
	// Skip evicted pending transactions (BlockHeight == -1): these are transient
	// wallet entries from EvictPendingTx that should not survive daemon restart.
	// Pending state resets on restart; only confirmed transactions are cached.
	// Detect synthetic entries (map key != tx.Hash) so they can be restored correctly.
	entries = make([]txCacheEntry, 0, len(w.transactions))
	for k, tx := range w.transactions {
		if tx.BlockHeight < 0 {
			continue // Skip evicted unconfirmed transactions
		}
		entries = append(entries, txCacheEntry{tx: tx, isSynthetic: k.Vout != 0})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].tx.BlockHeight != entries[j].tx.BlockHeight {
			return entries[i].tx.BlockHeight < entries[j].tx.BlockHeight
		}
		return entries[i].tx.Time.Before(entries[j].tx.Time)
	})

	// Reassign SeqNum to ensure contiguous ordering (1-based)
	for i, e := range entries {
		e.tx.SeqNum = int64(i + 1)
	}
	// nextSeqNum holds the last assigned value; notifications.go increments before use
	w.nextSeqNum = int64(len(entries))

	utxos = make([]*UTXO, 0, len(w.utxos))
	for _, utxo := range w.utxos {
		utxos = append(utxos, utxo)
	}

	balances = make(map[string]*Balance, len(w.balances))
	for addr, bal := range w.balances {
		balances[addr] = &Balance{
			Confirmed:   bal.Confirmed,
			Unconfirmed: bal.Unconfirmed,
			Immature:    bal.Immature,
		}
	}

	sortedAddrs = make([]string, 0, len(w.addresses))
	for addr := range w.addresses {
		sortedAddrs = append(sortedAddrs, addr)
	}
	sort.Strings(sortedAddrs)

	return entries, utxos, balances, sortedAddrs
}

// serializeCacheData encodes all cache sections into a checksummed byte slice.
func (w *Wallet) serializeCacheData(validationHash [checksumSize]byte, entries []txCacheEntry, utxos []*UTXO, balances map[string]*Balance) ([]byte, error) {
	var buf bytes.Buffer

	// Header: magic + version + validation hash
	if err := writeVarString(&buf, txCacheMagic); err != nil {
		return nil, fmt.Errorf("failed to write magic: %w", err)
	}
	if err := binary.Write(&buf, binary.LittleEndian, txCacheVersion); err != nil {
		return nil, fmt.Errorf("failed to write version: %w", err)
	}
	if _, err := buf.Write(validationHash[:]); err != nil {
		return nil, fmt.Errorf("failed to write validation hash: %w", err)
	}

	// Transactions
	if err := writeCompactSize(&buf, uint64(len(entries))); err != nil {
		return nil, fmt.Errorf("failed to write tx count: %w", err)
	}
	for _, e := range entries {
		if err := serializeWalletTx(&buf, e.tx, e.isSynthetic); err != nil {
			return nil, fmt.Errorf("failed to serialize tx: %w", err)
		}
	}

	// UTXOs
	if err := writeCompactSize(&buf, uint64(len(utxos))); err != nil {
		return nil, fmt.Errorf("failed to write utxo count: %w", err)
	}
	for _, utxo := range utxos {
		if err := serializeUTXO(&buf, utxo); err != nil {
			return nil, fmt.Errorf("failed to serialize utxo: %w", err)
		}
	}

	// Balances
	if err := writeCompactSize(&buf, uint64(len(balances))); err != nil {
		return nil, fmt.Errorf("failed to write balance count: %w", err)
	}
	for addr, bal := range balances {
		if err := serializeBalance(&buf, addr, bal); err != nil {
			return nil, fmt.Errorf("failed to serialize balance: %w", err)
		}
	}

	// Append SHA256 checksum
	checksum := sha256.Sum256(buf.Bytes())
	buf.Write(checksum[:])

	return buf.Bytes(), nil
}

// atomicWriteFile writes data to path via tmp file + fsync + rename.
func atomicWriteFile(path string, data []byte) error {
	tmpPath := path + ".tmp"

	file, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("failed to create temp cache: %w", err)
	}
	defer func() {
		// Cleanup on any error — file may already be closed
		if err != nil {
			file.Close()
			os.Remove(tmpPath)
		}
	}()

	if _, err = file.Write(data); err != nil {
		return fmt.Errorf("failed to write cache data: %w", err)
	}
	if err = file.Sync(); err != nil {
		return fmt.Errorf("failed to sync cache file: %w", err)
	}
	if err = file.Close(); err != nil {
		return fmt.Errorf("failed to close temp cache: %w", err)
	}
	if err = os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to rename cache: %w", err)
	}
	return nil
}

// computeAddressHash returns SHA256(sorted_addresses + bestBlockHash).
func computeAddressHash(sortedAddrs []string, bestBlockHash types.Hash) [checksumSize]byte {
	h := sha256.New()
	for _, addr := range sortedAddrs {
		h.Write([]byte(addr))
	}
	h.Write(bestBlockHash[:])
	var result [checksumSize]byte
	copy(result[:], h.Sum(nil))
	return result
}

// computeAddressHashLocked returns SHA256(sorted_addresses + bestBlockHash).
// Caller must hold w.mu.
func (w *Wallet) computeAddressHashLocked(bestBlockHash types.Hash) [checksumSize]byte {
	addrs := make([]string, 0, len(w.addresses))
	for addr := range w.addresses {
		addrs = append(addrs, addr)
	}
	sort.Strings(addrs)
	return computeAddressHash(addrs, bestBlockHash)
}

// --- Serialization helpers ---

func serializeWalletTx(w io.Writer, tx *WalletTransaction, isSynthetic bool) error {
	if _, err := w.Write(tx.Hash[:]); err != nil {
		return err
	}
	if _, err := w.Write(tx.BlockHash[:]); err != nil {
		return err
	}
	if err := binary.Write(w, binary.LittleEndian, tx.BlockHeight); err != nil {
		return err
	}
	if err := binary.Write(w, binary.LittleEndian, tx.Confirmations); err != nil {
		return err
	}
	if err := binary.Write(w, binary.LittleEndian, tx.Time.Unix()); err != nil {
		return err
	}
	if err := binary.Write(w, binary.LittleEndian, tx.SeqNum); err != nil {
		return err
	}
	if err := writeVarString(w, string(tx.Category)); err != nil {
		return err
	}
	if err := binary.Write(w, binary.LittleEndian, tx.Amount); err != nil {
		return err
	}
	if err := binary.Write(w, binary.LittleEndian, tx.Fee); err != nil {
		return err
	}
	if err := writeVarString(w, tx.Account); err != nil {
		return err
	}
	if err := writeVarString(w, tx.Address); err != nil {
		return err
	}
	if err := writeVarString(w, tx.Label); err != nil {
		return err
	}
	if err := writeVarString(w, tx.Comment); err != nil {
		return err
	}
	if err := writeVarString(w, tx.FromAddress); err != nil {
		return err
	}
	var flags byte
	if tx.WatchOnly {
		flags |= txFlagWatchOnly
	}
	if tx.IsConflicted {
		flags |= txFlagIsConflicted
	}
	if isSynthetic {
		flags |= txFlagIsSynthetic
	}
	if _, err := w.Write([]byte{flags}); err != nil {
		return err
	}
	return binary.Write(w, binary.LittleEndian, tx.Vout)
}

func deserializeWalletTx(r io.Reader) (*WalletTransaction, bool, error) {
	tx := &WalletTransaction{}

	if _, err := io.ReadFull(r, tx.Hash[:]); err != nil {
		return nil, false, err
	}
	if _, err := io.ReadFull(r, tx.BlockHash[:]); err != nil {
		return nil, false, err
	}
	if err := binary.Read(r, binary.LittleEndian, &tx.BlockHeight); err != nil {
		return nil, false, err
	}
	if err := binary.Read(r, binary.LittleEndian, &tx.Confirmations); err != nil {
		return nil, false, err
	}
	var unixTime int64
	if err := binary.Read(r, binary.LittleEndian, &unixTime); err != nil {
		return nil, false, err
	}
	tx.Time = time.Unix(unixTime, 0)
	if err := binary.Read(r, binary.LittleEndian, &tx.SeqNum); err != nil {
		return nil, false, err
	}
	cat, err := readVarString(r)
	if err != nil {
		return nil, false, err
	}
	tx.Category = TxCategory(cat)
	if err := binary.Read(r, binary.LittleEndian, &tx.Amount); err != nil {
		return nil, false, err
	}
	if err := binary.Read(r, binary.LittleEndian, &tx.Fee); err != nil {
		return nil, false, err
	}
	tx.Account, err = readVarString(r)
	if err != nil {
		return nil, false, err
	}
	tx.Address, err = readVarString(r)
	if err != nil {
		return nil, false, err
	}
	tx.Label, err = readVarString(r)
	if err != nil {
		return nil, false, err
	}
	tx.Comment, err = readVarString(r)
	if err != nil {
		return nil, false, err
	}
	tx.FromAddress, err = readVarString(r)
	if err != nil {
		return nil, false, err
	}
	var flags [1]byte
	if _, err := io.ReadFull(r, flags[:]); err != nil {
		return nil, false, err
	}
	tx.WatchOnly = flags[0]&txFlagWatchOnly != 0
	tx.IsConflicted = flags[0]&txFlagIsConflicted != 0
	isSynthetic := flags[0]&txFlagIsSynthetic != 0
	if err := binary.Read(r, binary.LittleEndian, &tx.Vout); err != nil {
		return nil, false, err
	}

	// Tx (*types.Transaction) intentionally nil — metadata-only cache
	return tx, isSynthetic, nil
}

func serializeUTXO(w io.Writer, utxo *UTXO) error {
	if _, err := w.Write(utxo.Outpoint.Hash[:]); err != nil {
		return err
	}
	if err := binary.Write(w, binary.LittleEndian, utxo.Outpoint.Index); err != nil {
		return err
	}
	if err := binary.Write(w, binary.LittleEndian, utxo.Output.Value); err != nil {
		return err
	}
	if err := writeCompactSize(w, uint64(len(utxo.Output.ScriptPubKey))); err != nil {
		return err
	}
	if _, err := w.Write(utxo.Output.ScriptPubKey); err != nil {
		return err
	}
	if err := binary.Write(w, binary.LittleEndian, utxo.BlockHeight); err != nil {
		return err
	}
	if err := binary.Write(w, binary.LittleEndian, utxo.BlockTime); err != nil {
		return err
	}
	if err := binary.Write(w, binary.LittleEndian, utxo.Confirmations); err != nil {
		return err
	}
	var flags byte
	if utxo.IsCoinbase {
		flags |= utxoFlagIsCoinbase
	}
	if utxo.IsStake {
		flags |= utxoFlagIsStake
	}
	if utxo.IsChange {
		flags |= utxoFlagIsChange
	}
	if utxo.Spendable {
		flags |= utxoFlagSpendable
	}
	if _, err := w.Write([]byte{flags}); err != nil {
		return err
	}
	if err := writeVarString(w, utxo.Address); err != nil {
		return err
	}
	return binary.Write(w, binary.LittleEndian, utxo.Account)
}

func deserializeUTXO(r io.Reader) (*UTXO, error) {
	utxo := &UTXO{
		Output: &types.TxOutput{},
	}

	if _, err := io.ReadFull(r, utxo.Outpoint.Hash[:]); err != nil {
		return nil, err
	}
	if err := binary.Read(r, binary.LittleEndian, &utxo.Outpoint.Index); err != nil {
		return nil, err
	}
	if err := binary.Read(r, binary.LittleEndian, &utxo.Output.Value); err != nil {
		return nil, err
	}
	scriptLen, err := readCompactSize(r)
	if err != nil {
		return nil, err
	}
	if scriptLen > maxScriptSize {
		return nil, fmt.Errorf("script too long: %d (max %d)", scriptLen, maxScriptSize)
	}
	utxo.Output.ScriptPubKey = make([]byte, scriptLen)
	if _, err := io.ReadFull(r, utxo.Output.ScriptPubKey); err != nil {
		return nil, err
	}
	if err := binary.Read(r, binary.LittleEndian, &utxo.BlockHeight); err != nil {
		return nil, err
	}
	if err := binary.Read(r, binary.LittleEndian, &utxo.BlockTime); err != nil {
		return nil, err
	}
	if err := binary.Read(r, binary.LittleEndian, &utxo.Confirmations); err != nil {
		return nil, err
	}
	var flags [1]byte
	if _, err := io.ReadFull(r, flags[:]); err != nil {
		return nil, err
	}
	utxo.IsCoinbase = flags[0]&utxoFlagIsCoinbase != 0
	utxo.IsStake = flags[0]&utxoFlagIsStake != 0
	utxo.IsChange = flags[0]&utxoFlagIsChange != 0
	utxo.Spendable = flags[0]&utxoFlagSpendable != 0
	utxo.Address, err = readVarString(r)
	if err != nil {
		return nil, err
	}
	if err := binary.Read(r, binary.LittleEndian, &utxo.Account); err != nil {
		return nil, err
	}
	return utxo, nil
}

func serializeBalance(w io.Writer, addr string, bal *Balance) error {
	if err := writeVarString(w, addr); err != nil {
		return err
	}
	if err := binary.Write(w, binary.LittleEndian, bal.Confirmed); err != nil {
		return err
	}
	if err := binary.Write(w, binary.LittleEndian, bal.Unconfirmed); err != nil {
		return err
	}
	return binary.Write(w, binary.LittleEndian, bal.Immature)
}

func deserializeBalance(r io.Reader) (string, *Balance, error) {
	addr, err := readVarString(r)
	if err != nil {
		return "", nil, err
	}
	bal := &Balance{}
	if err := binary.Read(r, binary.LittleEndian, &bal.Confirmed); err != nil {
		return "", nil, err
	}
	if err := binary.Read(r, binary.LittleEndian, &bal.Unconfirmed); err != nil {
		return "", nil, err
	}
	if err := binary.Read(r, binary.LittleEndian, &bal.Immature); err != nil {
		return "", nil, err
	}
	return addr, bal, nil
}

// --- Bitcoin compact size (VarInt) encoding ---
//
// Format (same as Bitcoin protocol):
//   < 253:       1 byte  (value directly)
//   <= 0xFFFF:   3 bytes (0xFD marker + uint16 LE)
//   <= 0xFFFFFFFF: 5 bytes (0xFE marker + uint32 LE)
//   else:        9 bytes (0xFF marker + uint64 LE)

func writeCompactSize(w io.Writer, n uint64) error {
	if n < 253 {
		return binary.Write(w, binary.LittleEndian, uint8(n))
	} else if n <= 0xFFFF {
		if err := binary.Write(w, binary.LittleEndian, uint8(253)); err != nil {
			return err
		}
		return binary.Write(w, binary.LittleEndian, uint16(n))
	} else if n <= 0xFFFFFFFF {
		if err := binary.Write(w, binary.LittleEndian, uint8(254)); err != nil {
			return err
		}
		return binary.Write(w, binary.LittleEndian, uint32(n))
	}
	if err := binary.Write(w, binary.LittleEndian, uint8(255)); err != nil {
		return err
	}
	return binary.Write(w, binary.LittleEndian, n)
}

func readCompactSize(r io.Reader) (uint64, error) {
	var first [1]byte
	if _, err := io.ReadFull(r, first[:]); err != nil {
		return 0, err
	}
	switch first[0] {
	case 253:
		var val uint16
		if err := binary.Read(r, binary.LittleEndian, &val); err != nil {
			return 0, err
		}
		return uint64(val), nil
	case 254:
		var val uint32
		if err := binary.Read(r, binary.LittleEndian, &val); err != nil {
			return 0, err
		}
		return uint64(val), nil
	case 255:
		var val uint64
		if err := binary.Read(r, binary.LittleEndian, &val); err != nil {
			return 0, err
		}
		return val, nil
	default:
		return uint64(first[0]), nil
	}
}

func writeVarString(w io.Writer, s string) error {
	if err := writeCompactSize(w, uint64(len(s))); err != nil {
		return err
	}
	_, err := w.Write([]byte(s))
	return err
}

func readVarString(r io.Reader) (string, error) {
	length, err := readCompactSize(r)
	if err != nil {
		return "", err
	}
	if length > maxVarStringSize {
		return "", fmt.Errorf("string too long: %d (max %d)", length, maxVarStringSize)
	}
	data := make([]byte, length)
	if _, err := io.ReadFull(r, data); err != nil {
		return "", err
	}
	return string(data), nil
}
