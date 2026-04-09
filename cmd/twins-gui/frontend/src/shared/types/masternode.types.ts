// MasternodeTier represents the 4 tiers of TWINS masternodes
// Names match backend tier names (internal/masternode/types.go)
export type MasternodeTier = 'bronze' | 'silver' | 'gold' | 'platinum';

export interface MasternodeConfig {
  tier: MasternodeTier;
  collateral: number;
  rewards: number;
  roi: number;
}

export type MasternodeStatus = 'ENABLED' | 'PRE_ENABLED' | 'EXPIRED' | 'VIN_SPENT' | 'REMOVE' | 'POS_ERROR' | 'MISSING' | 'WATCHDOG_EXPIRED' | 'NEW_START_REQUIRED' | 'UPDATE_REQUIRED' | 'SENTINEL_PING_EXPIRED';

export interface Masternode {
  id: string;
  alias: string;
  address: string;       // IP:port format
  protocol: number;      // Protocol version (e.g., 70914)
  status: MasternodeStatus;
  activeTime: number;    // Seconds active
  lastSeen: Date;        // Last seen timestamp (UTC)
  collateralAddress: string; // Collateral address derived from pubkey
  // Additional fields for internal use
  privateKey?: string;
  txHash: string;
  outputIndex: number;
  tier: MasternodeTier;
  rewards: number;
}

// Masternode tier configuration matching backend constants
// See internal/masternode/types.go for collateral amounts
export const MASTERNODE_TIERS: Record<MasternodeTier, MasternodeConfig> = {
  bronze: { tier: 'bronze', collateral: 1000000, rewards: 10, roi: 15.5 },
  silver: { tier: 'silver', collateral: 5000000, rewards: 55, roi: 17.1 },
  gold: { tier: 'gold', collateral: 20000000, rewards: 230, roi: 17.9 },
  platinum: { tier: 'platinum', collateral: 100000000, rewards: 1200, roi: 18.7 },
};

// NetworkMasternode represents a masternode on the network (from listmasternodes RPC)
// This is different from Masternode which represents user's configured masternodes
export interface NetworkMasternode {
  rank: number;              // Position in payment queue
  txhash: string;            // Collateral transaction hash
  outidx: number;            // Collateral output index
  status: string;            // ENABLED, PRE_ENABLED, EXPIRED, etc.
  addr: string;              // IP:Port address
  version: number;           // Protocol version
  lastseen: string;          // ISO timestamp when last seen
  activetime: number;        // Seconds since activation
  lastpaid: string;          // ISO timestamp of last payment
  tier: string;              // bronze, silver, gold, platinum
  paymentaddress: string;    // Address receiving rewards
  pubkey: string;            // Masternode public key
  pubkey_operator: string;   // Operator public key
}

// Network tier type (lowercase as returned by RPC)
export type NetworkMasternodeTier = 'bronze' | 'silver' | 'gold' | 'platinum' | '';

// Virtual columns derived from NetworkMasternode fields (not direct properties)
export type NetworkMasternodeVirtualColumn = 'network';

// Filter state for network masternodes
export interface NetworkMasternodeFilters {
  tier: 'all' | NetworkMasternodeTier;
  status: 'all' | string;
  search: string;
  sortColumn: keyof NetworkMasternode | NetworkMasternodeVirtualColumn | '';
  sortDirection: 'asc' | 'desc';
}

// MasternodeStatistics contains tier distribution and network stats
export interface MasternodeStatistics {
  tierCounts: Record<string, number>;      // bronze: X, silver: Y, etc.
  statusCounts: Record<string, number>;    // ENABLED: X, PRE_ENABLED: Y, etc.
  totalCount: number;
  enabledCount: number;
  totalCollateral: number;                 // In TWINS
  tierPercentages: Record<string, number>; // Percentages per tier
}

// Payment statistics entry for a single masternode address
export interface PaymentStatsEntry {
  address: string;        // TWINS address
  tier: string;           // bronze, silver, gold, platinum, or ""
  paymentCount: number;   // Total payments received
  totalPaid: number;      // Total paid in TWINS
  lastPaidTime: string;   // ISO timestamp of last payment
  latestTxID: string;     // Transaction ID of latest payment
}

// Payment statistics filter for backend sorting and pagination
export interface PaymentStatsFilter {
  sortColumn: string;    // address, tier, paymentCount, totalPaid, lastPaidTime, latestTxID
  sortDirection: string; // asc or desc
  page: number;          // 1-based page number
  pageSize: number;      // items per page (10, 25, 50, 100)
}

// Payment statistics response from backend
export interface PaymentStatsResponse {
  totalPaid: number;          // Total paid across all MNs in TWINS
  totalPayments: number;      // Total payment count
  uniquePaymentAddresses: number;  // Number of unique payment addresses (not necessarily unique masternodes)
  lowestBlock: number;        // Lowest scanned block height
  highestBlock: number;       // Highest scanned block height
  entries: PaymentStatsEntry[];
  totalEntries: number;       // Total entries before pagination
  totalPages: number;         // Total number of pages
  currentPage: number;        // Current 1-based page number
  pageSize: number;           // Items per page
}