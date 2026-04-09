export namespace config {
	
	export class Validation {
	    min?: number;
	    max?: number;
	    options?: string[];
	
	    static createFrom(source: any = {}) {
	        return new Validation(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.min = source["min"];
	        this.max = source["max"];
	        this.options = source["options"];
	    }
	}
	export class SettingMeta {
	    key: string;
	    type: number;
	    default: any;
	    category: string;
	    label: string;
	    description: string;
	    hotReload: boolean;
	    validation?: Validation;
	    cliFlag?: string;
	    envVar?: string;
	    units?: string;
	    deprecated?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new SettingMeta(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.key = source["key"];
	        this.type = source["type"];
	        this.default = source["default"];
	        this.category = source["category"];
	        this.label = source["label"];
	        this.description = source["description"];
	        this.hotReload = source["hotReload"];
	        this.validation = this.convertValues(source["validation"], Validation);
	        this.cliFlag = source["cliFlag"];
	        this.envVar = source["envVar"];
	        this.units = source["units"];
	        this.deprecated = source["deprecated"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace core {
	
	export class AddressUTXO {
	    txid: string;
	    vout: number;
	    amount: number;
	    confirmations: number;
	    block_height: number;
	
	    static createFrom(source: any = {}) {
	        return new AddressUTXO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.txid = source["txid"];
	        this.vout = source["vout"];
	        this.amount = source["amount"];
	        this.confirmations = source["confirmations"];
	        this.block_height = source["block_height"];
	    }
	}
	export class AddressTx {
	    txid: string;
	    block_height: number;
	    // Go type: time
	    time: any;
	    amount: number;
	    confirmations: number;
	
	    static createFrom(source: any = {}) {
	        return new AddressTx(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.txid = source["txid"];
	        this.block_height = source["block_height"];
	        this.time = this.convertValues(source["time"], null);
	        this.amount = source["amount"];
	        this.confirmations = source["confirmations"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class AddressInfo {
	    address: string;
	    balance: number;
	    total_received: number;
	    total_sent: number;
	    tx_count: number;
	    unconfirmed_balance: number;
	    transactions: AddressTx[];
	    utxos?: AddressUTXO[];
	
	    static createFrom(source: any = {}) {
	        return new AddressInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.address = source["address"];
	        this.balance = source["balance"];
	        this.total_received = source["total_received"];
	        this.total_sent = source["total_sent"];
	        this.tx_count = source["tx_count"];
	        this.unconfirmed_balance = source["unconfirmed_balance"];
	        this.transactions = this.convertValues(source["transactions"], AddressTx);
	        this.utxos = this.convertValues(source["utxos"], AddressUTXO);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class AddressTxPage {
	    transactions: AddressTx[];
	    total: number;
	    has_more: boolean;
	
	    static createFrom(source: any = {}) {
	        return new AddressTxPage(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.transactions = this.convertValues(source["transactions"], AddressTx);
	        this.total = source["total"];
	        this.has_more = source["has_more"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class AddressValidation {
	    isvalid: boolean;
	    address: string;
	    ismine: boolean;
	    iswatchonly: boolean;
	    isscript: boolean;
	    pubkey: string;
	    account: string;
	    hdkeypath: string;
	    hdmasterkeyid: string;
	
	    static createFrom(source: any = {}) {
	        return new AddressValidation(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.isvalid = source["isvalid"];
	        this.address = source["address"];
	        this.ismine = source["ismine"];
	        this.iswatchonly = source["iswatchonly"];
	        this.isscript = source["isscript"];
	        this.pubkey = source["pubkey"];
	        this.account = source["account"];
	        this.hdkeypath = source["hdkeypath"];
	        this.hdmasterkeyid = source["hdmasterkeyid"];
	    }
	}
	export class Balance {
	    total: number;
	    available: number;
	    spendable: number;
	    pending: number;
	    immature: number;
	    locked: number;
	
	    static createFrom(source: any = {}) {
	        return new Balance(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.total = source["total"];
	        this.available = source["available"];
	        this.spendable = source["spendable"];
	        this.pending = source["pending"];
	        this.immature = source["immature"];
	        this.locked = source["locked"];
	    }
	}
	export class Transaction {
	    txid: string;
	    vout: number;
	    amount: number;
	    fee: number;
	    confirmations: number;
	    block_hash: string;
	    block_height: number;
	    // Go type: time
	    time: any;
	    type: string;
	    address: string;
	    from_address: string;
	    label: string;
	    comment: string;
	    category: string;
	    is_watch_only: boolean;
	    is_locked: boolean;
	    is_conflicted: boolean;
	    is_coinbase: boolean;
	    is_coinstake: boolean;
	    matures_in: number;
	    debit: number;
	    credit: number;
	
	    static createFrom(source: any = {}) {
	        return new Transaction(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.txid = source["txid"];
	        this.vout = source["vout"];
	        this.amount = source["amount"];
	        this.fee = source["fee"];
	        this.confirmations = source["confirmations"];
	        this.block_hash = source["block_hash"];
	        this.block_height = source["block_height"];
	        this.time = this.convertValues(source["time"], null);
	        this.type = source["type"];
	        this.address = source["address"];
	        this.from_address = source["from_address"];
	        this.label = source["label"];
	        this.comment = source["comment"];
	        this.category = source["category"];
	        this.is_watch_only = source["is_watch_only"];
	        this.is_locked = source["is_locked"];
	        this.is_conflicted = source["is_conflicted"];
	        this.is_coinbase = source["is_coinbase"];
	        this.is_coinstake = source["is_coinstake"];
	        this.matures_in = source["matures_in"];
	        this.debit = source["debit"];
	        this.credit = source["credit"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class BlockDetail {
	    hash: string;
	    confirmations: number;
	    size: number;
	    height: number;
	    version: number;
	    merkleroot: string;
	    tx: Transaction[];
	    // Go type: time
	    time: any;
	    // Go type: time
	    mediantime: any;
	    nonce: number;
	    bits: string;
	    difficulty: number;
	    chainwork: string;
	    previousblockhash: string;
	    nextblockhash: string;
	    flags: string;
	    proofhash: string;
	    modifier: string;
	    txids: string[];
	    is_pos: boolean;
	    stake_reward: number;
	    masternode_reward: number;
	    staker_address: string;
	    masternode_address: string;
	    total_reward: number;
	
	    static createFrom(source: any = {}) {
	        return new BlockDetail(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.hash = source["hash"];
	        this.confirmations = source["confirmations"];
	        this.size = source["size"];
	        this.height = source["height"];
	        this.version = source["version"];
	        this.merkleroot = source["merkleroot"];
	        this.tx = this.convertValues(source["tx"], Transaction);
	        this.time = this.convertValues(source["time"], null);
	        this.mediantime = this.convertValues(source["mediantime"], null);
	        this.nonce = source["nonce"];
	        this.bits = source["bits"];
	        this.difficulty = source["difficulty"];
	        this.chainwork = source["chainwork"];
	        this.previousblockhash = source["previousblockhash"];
	        this.nextblockhash = source["nextblockhash"];
	        this.flags = source["flags"];
	        this.proofhash = source["proofhash"];
	        this.modifier = source["modifier"];
	        this.txids = source["txids"];
	        this.is_pos = source["is_pos"];
	        this.stake_reward = source["stake_reward"];
	        this.masternode_reward = source["masternode_reward"];
	        this.staker_address = source["staker_address"];
	        this.masternode_address = source["masternode_address"];
	        this.total_reward = source["total_reward"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class BlockSummary {
	    height: number;
	    hash: string;
	    // Go type: time
	    time: any;
	    tx_count: number;
	    size: number;
	    is_pos: boolean;
	    reward: number;
	
	    static createFrom(source: any = {}) {
	        return new BlockSummary(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.height = source["height"];
	        this.hash = source["hash"];
	        this.time = this.convertValues(source["time"], null);
	        this.tx_count = source["tx_count"];
	        this.size = source["size"];
	        this.is_pos = source["is_pos"];
	        this.reward = source["reward"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class BlockchainInfo {
	    chain: string;
	    blocks: number;
	    headers: number;
	    bestblockhash: string;
	    difficulty: number;
	    // Go type: time
	    mediantime: any;
	    verificationprogress: number;
	    chainwork: string;
	    pruned: boolean;
	    pruneheight: number;
	    initialblockdownload: boolean;
	    size_on_disk: number;
	    is_syncing: boolean;
	    is_out_of_sync: boolean;
	    behind_blocks: number;
	    behind_time: string;
	    sync_percentage: number;
	    current_block_scan: number;
	    peer_count: number;
	    is_connecting: boolean;
	
	    static createFrom(source: any = {}) {
	        return new BlockchainInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.chain = source["chain"];
	        this.blocks = source["blocks"];
	        this.headers = source["headers"];
	        this.bestblockhash = source["bestblockhash"];
	        this.difficulty = source["difficulty"];
	        this.mediantime = this.convertValues(source["mediantime"], null);
	        this.verificationprogress = source["verificationprogress"];
	        this.chainwork = source["chainwork"];
	        this.pruned = source["pruned"];
	        this.pruneheight = source["pruneheight"];
	        this.initialblockdownload = source["initialblockdownload"];
	        this.size_on_disk = source["size_on_disk"];
	        this.is_syncing = source["is_syncing"];
	        this.is_out_of_sync = source["is_out_of_sync"];
	        this.behind_blocks = source["behind_blocks"];
	        this.behind_time = source["behind_time"];
	        this.sync_percentage = source["sync_percentage"];
	        this.current_block_scan = source["current_block_scan"];
	        this.peer_count = source["peer_count"];
	        this.is_connecting = source["is_connecting"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class TxOutput {
	    index: number;
	    address: string;
	    amount: number;
	    script_type: string;
	    is_spent: boolean;
	
	    static createFrom(source: any = {}) {
	        return new TxOutput(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.index = source["index"];
	        this.address = source["address"];
	        this.amount = source["amount"];
	        this.script_type = source["script_type"];
	        this.is_spent = source["is_spent"];
	    }
	}
	export class TxInput {
	    txid: string;
	    vout: number;
	    address: string;
	    amount: number;
	    is_coinbase: boolean;
	
	    static createFrom(source: any = {}) {
	        return new TxInput(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.txid = source["txid"];
	        this.vout = source["vout"];
	        this.address = source["address"];
	        this.amount = source["amount"];
	        this.is_coinbase = source["is_coinbase"];
	    }
	}
	export class ExplorerTransaction {
	    txid: string;
	    block_hash: string;
	    block_height: number;
	    confirmations: number;
	    // Go type: time
	    time: any;
	    size: number;
	    fee: number;
	    is_coinbase: boolean;
	    is_coinstake: boolean;
	    inputs: TxInput[];
	    outputs: TxOutput[];
	    total_input: number;
	    total_output: number;
	    raw_hex?: string;
	
	    static createFrom(source: any = {}) {
	        return new ExplorerTransaction(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.txid = source["txid"];
	        this.block_hash = source["block_hash"];
	        this.block_height = source["block_height"];
	        this.confirmations = source["confirmations"];
	        this.time = this.convertValues(source["time"], null);
	        this.size = source["size"];
	        this.fee = source["fee"];
	        this.is_coinbase = source["is_coinbase"];
	        this.is_coinstake = source["is_coinstake"];
	        this.inputs = this.convertValues(source["inputs"], TxInput);
	        this.outputs = this.convertValues(source["outputs"], TxOutput);
	        this.total_input = source["total_input"];
	        this.total_output = source["total_output"];
	        this.raw_hex = source["raw_hex"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class LocalAddress {
	    address: string;
	    port: number;
	    score: number;
	
	    static createFrom(source: any = {}) {
	        return new LocalAddress(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.address = source["address"];
	        this.port = source["port"];
	        this.score = source["score"];
	    }
	}
	export class MasternodeInfo {
	    rank: number;
	    txhash: string;
	    outidx: number;
	    status: string;
	    addr: string;
	    version: number;
	    // Go type: time
	    lastseen: any;
	    activetime: number;
	    // Go type: time
	    lastpaid: any;
	    tier: string;
	    paymentaddress: string;
	    pubkey: string;
	    pubkey_operator: string;
	
	    static createFrom(source: any = {}) {
	        return new MasternodeInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.rank = source["rank"];
	        this.txhash = source["txhash"];
	        this.outidx = source["outidx"];
	        this.status = source["status"];
	        this.addr = source["addr"];
	        this.version = source["version"];
	        this.lastseen = this.convertValues(source["lastseen"], null);
	        this.activetime = source["activetime"];
	        this.lastpaid = this.convertValues(source["lastpaid"], null);
	        this.tier = source["tier"];
	        this.paymentaddress = source["paymentaddress"];
	        this.pubkey = source["pubkey"];
	        this.pubkey_operator = source["pubkey_operator"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class MyMasternode {
	    alias: string;
	    address: string;
	    protocol: number;
	    status: string;
	    active_seconds: number;
	    // Go type: time
	    last_seen: any;
	    collateral_address: string;
	    tx_hash: string;
	    output_index: number;
	
	    static createFrom(source: any = {}) {
	        return new MyMasternode(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.alias = source["alias"];
	        this.address = source["address"];
	        this.protocol = source["protocol"];
	        this.status = source["status"];
	        this.active_seconds = source["active_seconds"];
	        this.last_seen = this.convertValues(source["last_seen"], null);
	        this.collateral_address = source["collateral_address"];
	        this.tx_hash = source["tx_hash"];
	        this.output_index = source["output_index"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class NetworkType {
	    name: string;
	    limited: boolean;
	    reachable: boolean;
	    proxy: string;
	
	    static createFrom(source: any = {}) {
	        return new NetworkType(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.limited = source["limited"];
	        this.reachable = source["reachable"];
	        this.proxy = source["proxy"];
	    }
	}
	export class NetworkInfo {
	    version: number;
	    subversion: string;
	    protocolversion: number;
	    localservices: string;
	    localrelay: boolean;
	    timeoffset: number;
	    connections: number;
	    networkactive: boolean;
	    networks: NetworkType[];
	    relayfee: number;
	    localaddresses: LocalAddress[];
	    warnings: string;
	    network_height: number;
	
	    static createFrom(source: any = {}) {
	        return new NetworkInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.version = source["version"];
	        this.subversion = source["subversion"];
	        this.protocolversion = source["protocolversion"];
	        this.localservices = source["localservices"];
	        this.localrelay = source["localrelay"];
	        this.timeoffset = source["timeoffset"];
	        this.connections = source["connections"];
	        this.networkactive = source["networkactive"];
	        this.networks = this.convertValues(source["networks"], NetworkType);
	        this.relayfee = source["relayfee"];
	        this.localaddresses = this.convertValues(source["localaddresses"], LocalAddress);
	        this.warnings = source["warnings"];
	        this.network_height = source["network_height"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class OutPoint {
	    txid: string;
	    vout: number;
	
	    static createFrom(source: any = {}) {
	        return new OutPoint(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.txid = source["txid"];
	        this.vout = source["vout"];
	    }
	}
	export class PaymentRequest {
	    id: number;
	    // Go type: time
	    date: any;
	    label: string;
	    address: string;
	    message: string;
	    amount: number;
	
	    static createFrom(source: any = {}) {
	        return new PaymentRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.date = this.convertValues(source["date"], null);
	        this.label = source["label"];
	        this.address = source["address"];
	        this.message = source["message"];
	        this.amount = source["amount"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ReceivingAddress {
	    address: string;
	    label: string;
	    // Go type: time
	    created: any;
	
	    static createFrom(source: any = {}) {
	        return new ReceivingAddress(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.address = source["address"];
	        this.label = source["label"];
	        this.created = this.convertValues(source["created"], null);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ReceivingAddressFilter {
	    page: number;
	    page_size: number;
	    hide_zero_balance: boolean;
	    search_text: string;
	    sort_column: string;
	    sort_direction: string;
	
	    static createFrom(source: any = {}) {
	        return new ReceivingAddressFilter(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.page = source["page"];
	        this.page_size = source["page_size"];
	        this.hide_zero_balance = source["hide_zero_balance"];
	        this.search_text = source["search_text"];
	        this.sort_column = source["sort_column"];
	        this.sort_direction = source["sort_direction"];
	    }
	}
	export class ReceivingAddressRow {
	    address: string;
	    label: string;
	    balance: number;
	    has_payment_request: boolean;
	    // Go type: time
	    created: any;
	
	    static createFrom(source: any = {}) {
	        return new ReceivingAddressRow(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.address = source["address"];
	        this.label = source["label"];
	        this.balance = source["balance"];
	        this.has_payment_request = source["has_payment_request"];
	        this.created = this.convertValues(source["created"], null);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ReceivingAddressPage {
	    addresses: ReceivingAddressRow[];
	    total: number;
	    total_all: number;
	    page: number;
	    page_size: number;
	    total_pages: number;
	
	    static createFrom(source: any = {}) {
	        return new ReceivingAddressPage(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.addresses = this.convertValues(source["addresses"], ReceivingAddressRow);
	        this.total = source["total"];
	        this.total_all = source["total_all"];
	        this.page = source["page"];
	        this.page_size = source["page_size"];
	        this.total_pages = source["total_pages"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class SearchResult {
	    type: string;
	    query: string;
	    block?: BlockDetail;
	    transaction?: ExplorerTransaction;
	    address?: AddressInfo;
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new SearchResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.type = source["type"];
	        this.query = source["query"];
	        this.block = this.convertValues(source["block"], BlockDetail);
	        this.transaction = this.convertValues(source["transaction"], ExplorerTransaction);
	        this.address = this.convertValues(source["address"], AddressInfo);
	        this.error = source["error"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class SendError {
	    code: string;
	    message: string;
	    details?: string;
	
	    static createFrom(source: any = {}) {
	        return new SendError(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.code = source["code"];
	        this.message = source["message"];
	        this.details = source["details"];
	    }
	}
	export class StakingInfo {
	    enabled: boolean;
	    staking: boolean;
	    errors: string;
	    currentblocksize: number;
	    currentblocktx: number;
	    pooledtx: number;
	    difficulty: number;
	    "search-interval": number;
	    walletunlocked: boolean;
	    expectedstaketime: number;
	
	    static createFrom(source: any = {}) {
	        return new StakingInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.enabled = source["enabled"];
	        this.staking = source["staking"];
	        this.errors = source["errors"];
	        this.currentblocksize = source["currentblocksize"];
	        this.currentblocktx = source["currentblocktx"];
	        this.pooledtx = source["pooledtx"];
	        this.difficulty = source["difficulty"];
	        this["search-interval"] = source["search-interval"];
	        this.walletunlocked = source["walletunlocked"];
	        this.expectedstaketime = source["expectedstaketime"];
	    }
	}
	
	export class TransactionFilter {
	    page: number;
	    page_size: number;
	    date_filter: string;
	    date_range_from: string;
	    date_range_to: string;
	    type_filter: string;
	    search_text: string;
	    min_amount: number;
	    watch_only_filter: string;
	    hide_orphan_stakes: boolean;
	    sort_column: string;
	    sort_direction: string;
	
	    static createFrom(source: any = {}) {
	        return new TransactionFilter(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.page = source["page"];
	        this.page_size = source["page_size"];
	        this.date_filter = source["date_filter"];
	        this.date_range_from = source["date_range_from"];
	        this.date_range_to = source["date_range_to"];
	        this.type_filter = source["type_filter"];
	        this.search_text = source["search_text"];
	        this.min_amount = source["min_amount"];
	        this.watch_only_filter = source["watch_only_filter"];
	        this.hide_orphan_stakes = source["hide_orphan_stakes"];
	        this.sort_column = source["sort_column"];
	        this.sort_direction = source["sort_direction"];
	    }
	}
	export class TransactionPage {
	    transactions: Transaction[];
	    total: number;
	    total_all: number;
	    page: number;
	    page_size: number;
	    total_pages: number;
	
	    static createFrom(source: any = {}) {
	        return new TransactionPage(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.transactions = this.convertValues(source["transactions"], Transaction);
	        this.total = source["total"];
	        this.total_all = source["total_all"];
	        this.page = source["page"];
	        this.page_size = source["page_size"];
	        this.total_pages = source["total_pages"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	
	export class UTXO {
	    txid: string;
	    vout: number;
	    address: string;
	    label?: string;
	    scriptPubKey: string;
	    amount: number;
	    confirmations: number;
	    spendable: boolean;
	    solvable: boolean;
	    locked: boolean;
	    type: string;
	    date: number;
	    priority: number;
	
	    static createFrom(source: any = {}) {
	        return new UTXO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.txid = source["txid"];
	        this.vout = source["vout"];
	        this.address = source["address"];
	        this.label = source["label"];
	        this.scriptPubKey = source["scriptPubKey"];
	        this.amount = source["amount"];
	        this.confirmations = source["confirmations"];
	        this.spendable = source["spendable"];
	        this.solvable = source["solvable"];
	        this.locked = source["locked"];
	        this.type = source["type"];
	        this.date = source["date"];
	        this.priority = source["priority"];
	    }
	}

}

export namespace debug {
	
	export class MasternodeDetail {
	    outpoint: string;
	    address: string;
	    tier: string;
	    eventCount: number;
	
	    static createFrom(source: any = {}) {
	        return new MasternodeDetail(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.outpoint = source["outpoint"];
	        this.address = source["address"];
	        this.tier = source["tier"];
	        this.eventCount = source["eventCount"];
	    }
	}
	export class PeerDetail {
	    address: string;
	    eventCount: number;
	
	    static createFrom(source: any = {}) {
	        return new PeerDetail(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.address = source["address"];
	        this.eventCount = source["eventCount"];
	    }
	}
	export class ReasonCount {
	    label: string;
	    count: number;
	
	    static createFrom(source: any = {}) {
	        return new ReasonCount(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.label = source["label"];
	        this.count = source["count"];
	    }
	}
	export class SourceCount {
	    source: string;
	    count: number;
	
	    static createFrom(source: any = {}) {
	        return new SourceCount(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.source = source["source"];
	        this.count = source["count"];
	    }
	}
	export class StatusTransition {
	    timestamp: string;
	    from: string;
	    to: string;
	
	    static createFrom(source: any = {}) {
	        return new StatusTransition(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.timestamp = source["timestamp"];
	        this.from = source["from"];
	        this.to = source["to"];
	    }
	}
	export class Summary {
	    firstEvent: string;
	    lastEvent: string;
	    totalEvents: number;
	    fileSize: number;
	    sessionCount: number;
	    broadcastReceived: number;
	    broadcastAccepted: number;
	    broadcastRejected: number;
	    broadcastDedup: number;
	    acceptRate: number;
	    rejectReasons: ReasonCount[];
	    uniqueMasternodes: number;
	    tierBreakdown: Record<string, number>;
	    topSources: SourceCount[];
	    pingReceived: number;
	    pingAccepted: number;
	    pingFailed: number;
	    pingAcceptRate: number;
	    activePingsSent: number;
	    activePingsSuccess: number;
	    activePingsFailed: number;
	    dsegRequests: number;
	    dsegResponses: number;
	    avgMNsServed: number;
	    networkMNBCount: number;
	    networkMNPCount: number;
	    uniquePeers: number;
	    syncTransitions: StatusTransition[];
	    statusChanges: ReasonCount[];
	    activeMNChanges: StatusTransition[];
	    peerDetails: PeerDetail[];
	    masternodeDetails: MasternodeDetail[];
	
	    static createFrom(source: any = {}) {
	        return new Summary(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.firstEvent = source["firstEvent"];
	        this.lastEvent = source["lastEvent"];
	        this.totalEvents = source["totalEvents"];
	        this.fileSize = source["fileSize"];
	        this.sessionCount = source["sessionCount"];
	        this.broadcastReceived = source["broadcastReceived"];
	        this.broadcastAccepted = source["broadcastAccepted"];
	        this.broadcastRejected = source["broadcastRejected"];
	        this.broadcastDedup = source["broadcastDedup"];
	        this.acceptRate = source["acceptRate"];
	        this.rejectReasons = this.convertValues(source["rejectReasons"], ReasonCount);
	        this.uniqueMasternodes = source["uniqueMasternodes"];
	        this.tierBreakdown = source["tierBreakdown"];
	        this.topSources = this.convertValues(source["topSources"], SourceCount);
	        this.pingReceived = source["pingReceived"];
	        this.pingAccepted = source["pingAccepted"];
	        this.pingFailed = source["pingFailed"];
	        this.pingAcceptRate = source["pingAcceptRate"];
	        this.activePingsSent = source["activePingsSent"];
	        this.activePingsSuccess = source["activePingsSuccess"];
	        this.activePingsFailed = source["activePingsFailed"];
	        this.dsegRequests = source["dsegRequests"];
	        this.dsegResponses = source["dsegResponses"];
	        this.avgMNsServed = source["avgMNsServed"];
	        this.networkMNBCount = source["networkMNBCount"];
	        this.networkMNPCount = source["networkMNPCount"];
	        this.uniquePeers = source["uniquePeers"];
	        this.syncTransitions = this.convertValues(source["syncTransitions"], StatusTransition);
	        this.statusChanges = this.convertValues(source["statusChanges"], ReasonCount);
	        this.activeMNChanges = this.convertValues(source["activeMNChanges"], StatusTransition);
	        this.peerDetails = this.convertValues(source["peerDetails"], PeerDetail);
	        this.masternodeDetails = this.convertValues(source["masternodeDetails"], MasternodeDetail);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace initialization {
	
	export class DiskSpaceInfo {
	    available: number;
	    required: number;
	    hasSpace: boolean;
	
	    static createFrom(source: any = {}) {
	        return new DiskSpaceInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.available = source["available"];
	        this.required = source["required"];
	        this.hasSpace = source["hasSpace"];
	    }
	}
	export class MasternodeEntry {
	    alias: string;
	    address: string;
	    privkey: string;
	    txid: string;
	    outputIndex: number;
	
	    static createFrom(source: any = {}) {
	        return new MasternodeEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.alias = source["alias"];
	        this.address = source["address"];
	        this.privkey = source["privkey"];
	        this.txid = source["txid"];
	        this.outputIndex = source["outputIndex"];
	    }
	}

}

export namespace keys {
	
	export class Accelerator {
	    Key: string;
	    Modifiers: string[];
	
	    static createFrom(source: any = {}) {
	        return new Accelerator(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Key = source["Key"];
	        this.Modifiers = source["Modifiers"];
	    }
	}

}

export namespace main {
	
	export class AddPeerResult {
	    line: string;
	    address: string;
	    alias: string;
	    success: boolean;
	    error: string;
	
	    static createFrom(source: any = {}) {
	        return new AddPeerResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.line = source["line"];
	        this.address = source["address"];
	        this.alias = source["alias"];
	        this.success = source["success"];
	        this.error = source["error"];
	    }
	}
	export class BannedPeerInfo {
	    address: string;
	    alias: string;
	    bannedUntil: number;
	    banCreated: number;
	    reason: string;
	
	    static createFrom(source: any = {}) {
	        return new BannedPeerInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.address = source["address"];
	        this.alias = source["alias"];
	        this.bannedUntil = source["bannedUntil"];
	        this.banCreated = source["banCreated"];
	        this.reason = source["reason"];
	    }
	}
	export class ChangePassphraseRequest {
	    oldPassphrase: string;
	    newPassphrase: string;
	
	    static createFrom(source: any = {}) {
	        return new ChangePassphraseRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.oldPassphrase = source["oldPassphrase"];
	        this.newPassphrase = source["newPassphrase"];
	    }
	}
	export class ChangePassphraseResult {
	    success: boolean;
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new ChangePassphraseResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.success = source["success"];
	        this.error = source["error"];
	    }
	}
	export class Contact {
	    label: string;
	    address: string;
	    created: string;
	
	    static createFrom(source: any = {}) {
	        return new Contact(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.label = source["label"];
	        this.address = source["address"];
	        this.created = source["created"];
	    }
	}
	export class DataDirectoryInfo {
	    path: string;
	    source: string;
	
	    static createFrom(source: any = {}) {
	        return new DataDirectoryInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.source = source["source"];
	    }
	}
	export class DebugEvent {
	    timestamp: string;
	    type: string;
	    category: string;
	    source: string;
	    summary: string;
	    payload: string;
	
	    static createFrom(source: any = {}) {
	        return new DebugEvent(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.timestamp = source["timestamp"];
	        this.type = source["type"];
	        this.category = source["category"];
	        this.source = source["source"];
	        this.summary = source["summary"];
	        this.payload = source["payload"];
	    }
	}
	export class DebugFilter {
	    category?: string;
	    type?: string;
	    source?: string;
	    search?: string;
	    limit?: number;
	
	    static createFrom(source: any = {}) {
	        return new DebugFilter(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.category = source["category"];
	        this.type = source["type"];
	        this.source = source["source"];
	        this.search = source["search"];
	        this.limit = source["limit"];
	    }
	}
	export class DebugStatusResponse {
	    enabled: boolean;
	    total: number;
	    byCategory: Record<string, number>;
	    fileSize: number;
	
	    static createFrom(source: any = {}) {
	        return new DebugStatusResponse(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.enabled = source["enabled"];
	        this.total = source["total"];
	        this.byCategory = source["byCategory"];
	        this.fileSize = source["fileSize"];
	    }
	}
	export class EncryptWalletRequest {
	    passphrase: string;
	
	    static createFrom(source: any = {}) {
	        return new EncryptWalletRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.passphrase = source["passphrase"];
	    }
	}
	export class EncryptWalletResult {
	    success: boolean;
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new EncryptWalletResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.success = source["success"];
	        this.error = source["error"];
	    }
	}
	export class SendTransactionOptions {
	    selectedUtxos: string[];
	    changeAddress: string;
	    splitCount: number;
	    feeRate: number;
	
	    static createFrom(source: any = {}) {
	        return new SendTransactionOptions(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.selectedUtxos = source["selectedUtxos"];
	        this.changeAddress = source["changeAddress"];
	        this.splitCount = source["splitCount"];
	        this.feeRate = source["feeRate"];
	    }
	}
	export class FeeEstimateRequest {
	    recipients: Record<string, number>;
	    options?: SendTransactionOptions;
	
	    static createFrom(source: any = {}) {
	        return new FeeEstimateRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.recipients = source["recipients"];
	        this.options = this.convertValues(source["options"], SendTransactionOptions);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class FeeEstimateResult {
	    fee: number;
	    inputCount: number;
	    txSize: number;
	
	    static createFrom(source: any = {}) {
	        return new FeeEstimateResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.fee = source["fee"];
	        this.inputCount = source["inputCount"];
	        this.txSize = source["txSize"];
	    }
	}
	export class LockWalletResult {
	    success: boolean;
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new LockWalletResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.success = source["success"];
	        this.error = source["error"];
	    }
	}
	export class MasternodeCollateralInfo {
	    isCollateral: boolean;
	    alias?: string;
	
	    static createFrom(source: any = {}) {
	        return new MasternodeCollateralInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.isCollateral = source["isCollateral"];
	        this.alias = source["alias"];
	    }
	}
	export class MasternodeConfigEntry {
	    alias: string;
	    ip: string;
	    privateKey: string;
	    txHash: string;
	    outputIndex: number;
	
	    static createFrom(source: any = {}) {
	        return new MasternodeConfigEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.alias = source["alias"];
	        this.ip = source["ip"];
	        this.privateKey = source["privateKey"];
	        this.txHash = source["txHash"];
	        this.outputIndex = source["outputIndex"];
	    }
	}
	export class MasternodeOutput {
	    txHash: string;
	    outputIndex: number;
	    amount: number;
	    tier: string;
	    confirmations: number;
	    isReady: boolean;
	
	    static createFrom(source: any = {}) {
	        return new MasternodeOutput(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.txHash = source["txHash"];
	        this.outputIndex = source["outputIndex"];
	        this.amount = source["amount"];
	        this.tier = source["tier"];
	        this.confirmations = source["confirmations"];
	        this.isReady = source["isReady"];
	    }
	}
	export class MasternodeStatistics {
	    tierCounts: Record<string, number>;
	    statusCounts: Record<string, number>;
	    totalCount: number;
	    enabledCount: number;
	    totalCollateral: number;
	    tierPercentages: Record<string, number>;
	
	    static createFrom(source: any = {}) {
	        return new MasternodeStatistics(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.tierCounts = source["tierCounts"];
	        this.statusCounts = source["statusCounts"];
	        this.totalCount = source["totalCount"];
	        this.enabledCount = source["enabledCount"];
	        this.totalCollateral = source["totalCollateral"];
	        this.tierPercentages = source["tierPercentages"];
	    }
	}
	export class PaymentStatsEntry {
	    address: string;
	    tier: string;
	    paymentCount: number;
	    totalPaid: number;
	    lastPaidTime: string;
	    latestTxID: string;
	
	    static createFrom(source: any = {}) {
	        return new PaymentStatsEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.address = source["address"];
	        this.tier = source["tier"];
	        this.paymentCount = source["paymentCount"];
	        this.totalPaid = source["totalPaid"];
	        this.lastPaidTime = source["lastPaidTime"];
	        this.latestTxID = source["latestTxID"];
	    }
	}
	export class PaymentStatsFilter {
	    sortColumn: string;
	    sortDirection: string;
	    page: number;
	    pageSize: number;
	
	    static createFrom(source: any = {}) {
	        return new PaymentStatsFilter(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.sortColumn = source["sortColumn"];
	        this.sortDirection = source["sortDirection"];
	        this.page = source["page"];
	        this.pageSize = source["pageSize"];
	    }
	}
	export class PaymentStatsResponse {
	    totalPaid: number;
	    totalPayments: number;
	    uniquePaymentAddresses: number;
	    lowestBlock: number;
	    highestBlock: number;
	    entries: PaymentStatsEntry[];
	    totalEntries: number;
	    totalPages: number;
	    currentPage: number;
	    pageSize: number;
	
	    static createFrom(source: any = {}) {
	        return new PaymentStatsResponse(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.totalPaid = source["totalPaid"];
	        this.totalPayments = source["totalPayments"];
	        this.uniquePaymentAddresses = source["uniquePaymentAddresses"];
	        this.lowestBlock = source["lowestBlock"];
	        this.highestBlock = source["highestBlock"];
	        this.entries = this.convertValues(source["entries"], PaymentStatsEntry);
	        this.totalEntries = source["totalEntries"];
	        this.totalPages = source["totalPages"];
	        this.currentPage = source["currentPage"];
	        this.pageSize = source["pageSize"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class PeerDetail {
	    id: number;
	    address: string;
	    alias: string;
	    services: string;
	    lastSend: number;
	    lastRecv: number;
	    bytesSent: number;
	    bytesReceived: number;
	    connTime: number;
	    timeOffset: number;
	    pingTime: number;
	    pingWait: number;
	    protocolVersion: number;
	    userAgent: string;
	    inbound: boolean;
	    startHeight: number;
	    banScore: number;
	    syncedHeaders: number;
	    syncedBlocks: number;
	    syncedHeight: number;
	    whitelisted: boolean;
	
	    static createFrom(source: any = {}) {
	        return new PeerDetail(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.address = source["address"];
	        this.alias = source["alias"];
	        this.services = source["services"];
	        this.lastSend = source["lastSend"];
	        this.lastRecv = source["lastRecv"];
	        this.bytesSent = source["bytesSent"];
	        this.bytesReceived = source["bytesReceived"];
	        this.connTime = source["connTime"];
	        this.timeOffset = source["timeOffset"];
	        this.pingTime = source["pingTime"];
	        this.pingWait = source["pingWait"];
	        this.protocolVersion = source["protocolVersion"];
	        this.userAgent = source["userAgent"];
	        this.inbound = source["inbound"];
	        this.startHeight = source["startHeight"];
	        this.banScore = source["banScore"];
	        this.syncedHeaders = source["syncedHeaders"];
	        this.syncedBlocks = source["syncedBlocks"];
	        this.syncedHeight = source["syncedHeight"];
	        this.whitelisted = source["whitelisted"];
	    }
	}
	export class RPCCommandResult {
	    result?: any;
	    error?: string;
	    time: string;
	
	    static createFrom(source: any = {}) {
	        return new RPCCommandResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.result = source["result"];
	        this.error = source["error"];
	        this.time = source["time"];
	    }
	}
	export class RepairResult {
	    action: string;
	    success: boolean;
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new RepairResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.action = source["action"];
	        this.success = source["success"];
	        this.error = source["error"];
	    }
	}
	export class RestoreToStakingOnlyModeResult {
	    success: boolean;
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new RestoreToStakingOnlyModeResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.success = source["success"];
	        this.error = source["error"];
	    }
	}
	export class SendTransactionMultiRequest {
	    recipients: Record<string, number>;
	    options?: SendTransactionOptions;
	
	    static createFrom(source: any = {}) {
	        return new SendTransactionMultiRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.recipients = source["recipients"];
	        this.options = this.convertValues(source["options"], SendTransactionOptions);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class SendTransactionResult {
	    txid?: string;
	    error?: core.SendError;
	
	    static createFrom(source: any = {}) {
	        return new SendTransactionResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.txid = source["txid"];
	        this.error = this.convertValues(source["error"], core.SendError);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class SettingMetadata {
	    key: string;
	    tab: string;
	    requiresRestart: boolean;
	    overriddenByCLI: boolean;
	    cliFlagName?: string;
	    defaultValue?: any;
	    minValue?: any;
	    maxValue?: any;
	    deprecated?: boolean;
	    deprecatedMsg?: string;
	
	    static createFrom(source: any = {}) {
	        return new SettingMetadata(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.key = source["key"];
	        this.tab = source["tab"];
	        this.requiresRestart = source["requiresRestart"];
	        this.overriddenByCLI = source["overriddenByCLI"];
	        this.cliFlagName = source["cliFlagName"];
	        this.defaultValue = source["defaultValue"];
	        this.minValue = source["minValue"];
	        this.maxValue = source["maxValue"];
	        this.deprecated = source["deprecated"];
	        this.deprecatedMsg = source["deprecatedMsg"];
	    }
	}
	export class ThemeInfo {
	    name: string;
	    isBuiltIn: boolean;
	    path?: string;
	
	    static createFrom(source: any = {}) {
	        return new ThemeInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.isBuiltIn = source["isBuiltIn"];
	        this.path = source["path"];
	    }
	}
	export class ToolsInfo {
	    clientName: string;
	    clientVersion: string;
	    goVersion: string;
	    platform: string;
	    buildDate: string;
	    databaseVersion: string;
	    startupTime: number;
	    dataDir: string;
	    networkName: string;
	    connections: number;
	    inPeers: number;
	    outPeers: number;
	    blockCount: number;
	    lastBlockTime: number;
	    masternodeCount: number;
	
	    static createFrom(source: any = {}) {
	        return new ToolsInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.clientName = source["clientName"];
	        this.clientVersion = source["clientVersion"];
	        this.goVersion = source["goVersion"];
	        this.platform = source["platform"];
	        this.buildDate = source["buildDate"];
	        this.databaseVersion = source["databaseVersion"];
	        this.startupTime = source["startupTime"];
	        this.dataDir = source["dataDir"];
	        this.networkName = source["networkName"];
	        this.connections = source["connections"];
	        this.inPeers = source["inPeers"];
	        this.outPeers = source["outPeers"];
	        this.blockCount = source["blockCount"];
	        this.lastBlockTime = source["lastBlockTime"];
	        this.masternodeCount = source["masternodeCount"];
	    }
	}
	export class TrafficInfo {
	    totalBytesRecv: number;
	    totalBytesSent: number;
	    peerCount: number;
	
	    static createFrom(source: any = {}) {
	        return new TrafficInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.totalBytesRecv = source["totalBytesRecv"];
	        this.totalBytesSent = source["totalBytesSent"];
	        this.peerCount = source["peerCount"];
	    }
	}
	export class TrafficSample {
	    timestamp: number;
	    bytesIn: number;
	    bytesOut: number;
	    rateIn: number;
	    rateOut: number;
	
	    static createFrom(source: any = {}) {
	        return new TrafficSample(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.timestamp = source["timestamp"];
	        this.bytesIn = source["bytesIn"];
	        this.bytesOut = source["bytesOut"];
	        this.rateIn = source["rateIn"];
	        this.rateOut = source["rateOut"];
	    }
	}
	export class UnlockWalletRequest {
	    passphrase: string;
	    timeout: number;
	    stakingOnly: boolean;
	
	    static createFrom(source: any = {}) {
	        return new UnlockWalletRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.passphrase = source["passphrase"];
	        this.timeout = source["timeout"];
	        this.stakingOnly = source["stakingOnly"];
	    }
	}
	export class UnlockWalletResult {
	    success: boolean;
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new UnlockWalletResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.success = source["success"];
	        this.error = source["error"];
	    }
	}
	export class WalletStatus {
	    encrypted: boolean;
	    unlocked: boolean;
	
	    static createFrom(source: any = {}) {
	        return new WalletStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.encrypted = source["encrypted"];
	        this.unlocked = source["unlocked"];
	    }
	}

}

export namespace menu {
	
	export class MenuItem {
	    Label: string;
	    Role: number;
	    Accelerator?: keys.Accelerator;
	    Type: string;
	    Disabled: boolean;
	    Hidden: boolean;
	    Checked: boolean;
	    SubMenu?: Menu;
	
	    static createFrom(source: any = {}) {
	        return new MenuItem(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Label = source["Label"];
	        this.Role = source["Role"];
	        this.Accelerator = this.convertValues(source["Accelerator"], keys.Accelerator);
	        this.Type = source["Type"];
	        this.Disabled = source["Disabled"];
	        this.Hidden = source["Hidden"];
	        this.Checked = source["Checked"];
	        this.SubMenu = this.convertValues(source["SubMenu"], Menu);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Menu {
	    Items: MenuItem[];
	
	    static createFrom(source: any = {}) {
	        return new Menu(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Items = this.convertValues(source["Items"], MenuItem);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace preferences {
	
	export class WindowState {
	    x: number;
	    y: number;
	    width: number;
	    height: number;
	    maximized: boolean;
	
	    static createFrom(source: any = {}) {
	        return new WindowState(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.x = source["x"];
	        this.y = source["y"];
	        this.width = source["width"];
	        this.height = source["height"];
	        this.maximized = source["maximized"];
	    }
	}
	export class GUISettings {
	    fMinimizeToTray: boolean;
	    fMinimizeOnClose: boolean;
	    nDisplayUnit: number;
	    theme: string;
	    digits: number;
	    language: string;
	    fHideTrayIcon: boolean;
	    fShowMasternodesTab: boolean;
	    strThirdPartyTxUrls: string;
	    windowGeometry: Record<string, WindowState>;
	    nStakeSplitThreshold: number;
	    fCoinControlFeatures: boolean;
	    nCoinControlMode: number;
	    nCoinControlSortColumn: number;
	    nCoinControlSortOrder: number;
	    transactionDate: number;
	    transactionType: number;
	    transactionMinAmount: number;
	    fHideOrphans: boolean;
	    fHideZeroBalances: boolean;
	    fFeeSectionMinimized: boolean;
	    nFeeRadio: number;
	    nCustomFeeRadio: number;
	    nSmartFeeSliderPosition: number;
	    nTransactionFee: number;
	    fPayOnlyMinFee: boolean;
	    fSendFreeTransactions: boolean;
	    fSubtractFeeFromAmount: boolean;
	    current_receive_address: string;
	    fRestartRequired: boolean;
	    strDataDir: string;
	    _version: number;
	    _lastModified: string;
	
	    static createFrom(source: any = {}) {
	        return new GUISettings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.fMinimizeToTray = source["fMinimizeToTray"];
	        this.fMinimizeOnClose = source["fMinimizeOnClose"];
	        this.nDisplayUnit = source["nDisplayUnit"];
	        this.theme = source["theme"];
	        this.digits = source["digits"];
	        this.language = source["language"];
	        this.fHideTrayIcon = source["fHideTrayIcon"];
	        this.fShowMasternodesTab = source["fShowMasternodesTab"];
	        this.strThirdPartyTxUrls = source["strThirdPartyTxUrls"];
	        this.windowGeometry = this.convertValues(source["windowGeometry"], WindowState, true);
	        this.nStakeSplitThreshold = source["nStakeSplitThreshold"];
	        this.fCoinControlFeatures = source["fCoinControlFeatures"];
	        this.nCoinControlMode = source["nCoinControlMode"];
	        this.nCoinControlSortColumn = source["nCoinControlSortColumn"];
	        this.nCoinControlSortOrder = source["nCoinControlSortOrder"];
	        this.transactionDate = source["transactionDate"];
	        this.transactionType = source["transactionType"];
	        this.transactionMinAmount = source["transactionMinAmount"];
	        this.fHideOrphans = source["fHideOrphans"];
	        this.fHideZeroBalances = source["fHideZeroBalances"];
	        this.fFeeSectionMinimized = source["fFeeSectionMinimized"];
	        this.nFeeRadio = source["nFeeRadio"];
	        this.nCustomFeeRadio = source["nCustomFeeRadio"];
	        this.nSmartFeeSliderPosition = source["nSmartFeeSliderPosition"];
	        this.nTransactionFee = source["nTransactionFee"];
	        this.fPayOnlyMinFee = source["fPayOnlyMinFee"];
	        this.fSendFreeTransactions = source["fSendFreeTransactions"];
	        this.fSubtractFeeFromAmount = source["fSubtractFeeFromAmount"];
	        this.current_receive_address = source["current_receive_address"];
	        this.fRestartRequired = source["fRestartRequired"];
	        this.strDataDir = source["strDataDir"];
	        this._version = source["_version"];
	        this._lastModified = source["_lastModified"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

