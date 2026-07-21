/**
 * app.js (v6 - Hybrid Sync/Async SDVN API Gateway)
 *
 * Optimizations applied for NS-3 Simulation:
 * 1. Strict Singleton Connection.
 * 2. Hybrid Invokes: 
 * - Waits for Consensus for Registration/Setup functions.
 * - Fire-and-Forget (Instant 202) for high-frequency Hash/Trust logs.
 */

'use strict';

const express    = require('express');
const bodyParser = require('body-parser');
const { Gateway, Wallets } = require('fabric-network');
const path       = require('path');
const fs         = require('fs');

const app = express();
app.use(bodyParser.json({ limit: '10mb' })); 
app.use(bodyParser.urlencoded({ limit: '10mb', extended: true }));

const channelName   = 'mychannel';
const chaincodeName = 'sdvncc';

// Global variables for persistent gRPC connection
let globalGateway = null;
let globalContract = null;

// ─── 1. INITIALIZE FABRIC CONNECTION ON STARTUP ───────────────────────────
async function initFabric() {
    try {
        console.log('Initializing Hyperledger Fabric connection...');
        const ccpPath = path.resolve(
            __dirname, '..', 'test-network', 'organizations',
            'peerOrganizations', 'org1.example.com', 'connection-org1.json'
        );
        const ccp = JSON.parse(fs.readFileSync(ccpPath, 'utf8'));
        const walletPath = path.join(__dirname, 'wallet');
        const wallet = await Wallets.newFileSystemWallet(walletPath);

        const identity = await wallet.get('admin');
        if (!identity) {
            console.error('Admin identity not found in wallet. Run enrollAdmin.js first!');
            return false;
        }

        globalGateway = new Gateway();
        
        await globalGateway.connect(ccp, {
            wallet,
            identity: 'admin',
            discovery: { enabled: true, asLocalhost: true }
        });

        const network = await globalGateway.getNetwork(channelName);
        globalContract = network.getContract(chaincodeName);
        
        console.log('✅ Hyperledger Fabric connected successfully. Contract ready.');
        return true;
    } catch (error) {
        console.error(`❌ Failed to connect to Fabric: ${error.message}`);
        return false;
    }
}

// ─── 1b. PER-ENTITY SERIALIZATION + MVCC RETRY ────────────────────────────
//
// Fabric uses optimistic concurrency (MVCC): if two transactions in the same
// block read-then-write (or write/write) the SAME key, only the first commits;
// the rest fail with MVCC_READ_CONFLICT. NS-3 fires writes fire-and-forget, so
// multiple transactions touching the same on-chain entity (e.g. RecordCCSignatures
// then UpdateControllerTrustScore for controller C0 — the latter READS the ccsig
// key the former WRITES) were landing in one block and colliding.
//
// Fix: funnel every write that touches a given entity through a single-file
// promise chain keyed by that entity, so the next same-entity submit only starts
// after the previous one has COMMITTED (submitTransaction resolves post-commit).
// Writes to different entities still run in parallel. A bounded retry with jittered
// backoff mops up any residual conflict (e.g. against a startup registration).

const keyChains = new Map(); // serialization key -> tail Promise

// The conflict domain is the on-chain entity. args[0] is the vehicle ("V5") or
// controller ("C0") id for every per-entity write, so key on it — this groups
// RecordCCSignatures+UpdateControllerTrustScore (C0) and UpdateTrustScore+
// WriteAuditLog+RegisterFlowRule (V5) onto the same lane. Everything else
// (SubmitMessageHashBatch, RecordIPFSAvailability, LogIncident) gets a stable
// per-function lane so its own repeats serialize without blocking other work.
function serialKeyFor(fcn, args) {
    const id = args[0];
    if (typeof id === 'string' && /^[CV]\d+$/.test(id)) return 'entity:' + id;
    return 'fcn:' + fcn;
}

async function submitWithRetry(fcn, args, maxRetries = 6) {
    for (let attempt = 0; ; attempt++) {
        try {
            await globalContract.submitTransaction(fcn, ...args);
            return;
        } catch (err) {
            const msg = (err && err.message) || '';
            const transient = msg.includes('MVCC_READ_CONFLICT') ||
                              msg.includes('PHANTOM_READ_CONFLICT') ||
                              msg.includes('ENDORSEMENT_POLICY_FAILURE');
            if (transient && attempt < maxRetries) {
                const backoff = 120 * (attempt + 1) + Math.floor(Math.random() * 120);
                await new Promise(r => setTimeout(r, backoff));
                continue;
            }
            throw err;
        }
    }
}

// Chain the submit onto its entity lane and return the promise. A prior failure
// on the lane must not break the chain, so we swallow it before chaining.
function enqueueSerial(fcn, args) {
    const key = serialKeyFor(fcn, args);
    const prev = keyChains.get(key) || Promise.resolve();
    const next = prev.catch(() => {}).then(() => submitWithRetry(fcn, args));
    keyChains.set(key, next);
    // Drop the lane once it drains so the Map does not grow unbounded.
    next.catch(() => {}).finally(() => {
        if (keyChains.get(key) === next) keyChains.delete(key);
    });
    return next;
}

// ─── 2. WRITE OPERATIONS (HYBRID: SYNC vs ASYNC) ──────────────────────────
app.post('/invoke/:fcn', async (req, res) => {
    const fcn = req.params.fcn;
    
    // Parse arguments
    let args = req.body.args || req.body;
    if (!Array.isArray(args)) {
        args = Object.values(args).map(arg => String(arg));
    } else {
        args = args.map(arg => String(arg));
    }

    if (!globalContract) {
        return res.status(500).json({ error: "Contract not initialized." });
    }

    // DEFINE WHICH FUNCTIONS MUST WAIT FOR BLOCKCHAIN CONSENSUS
    const syncFunctions = [
        'RegisterVehicle', 
        'RegisterControllerKey', 
        'SetSystemConfig', 
        'SeedEndorserSet',
        'RegisterDkgPeer',     // must commit before first ceremony
        'RevokeVehicle',       // eviction must be committed before rekey
        'RecordDkgCeremony'    // ceremony record before key distribution
    ];

    if (syncFunctions.includes(fcn)) {
        // =================================================================
        // SYNCHRONOUS MODE: Block NS-3 until the ledger is fully updated
        // =================================================================
        console.log(`[INVOKE SYNC] Processing : ${fcn} | Waiting for consensus...`);
        try {
            await enqueueSerial(fcn, args);
            console.log(`[INVOKE SUCCESS] ${fcn} securely committed to ledger.`);
            res.status(200).json({ status: 'success', message: `Transaction ${fcn} committed.` });
        } catch (error) {
            console.error(`[INVOKE ERROR] ${fcn} failed: ${error.message}`);
            res.status(500).json({ error: error.message });
        }
    } else {
        // =================================================================
        // ASYNCHRONOUS MODE: Fire-and-Forget to keep NS-3 running fast
        // =================================================================
        res.status(202).json({ status: 'queued', message: `Transaction ${fcn} accepted.` });
        console.log(`[INVOKE ASYNC] Queued : ${fcn} | Simulation unblocked.`);

        // Process in background, serialized per entity so same-key writes never
        // collide in a block (MVCC_READ_CONFLICT), with bounded retry on conflict.
        enqueueSerial(fcn, args)
            .catch(error => {
                console.error(`[ASYNC ERROR] ${fcn} failed in background: ${error.message}`);
            });
    }
});

// ─── 3. READ OPERATIONS (EVALUATE - FAST) ─────────────────────────────────
app.get('/evaluate/:fcn', async (req, res) => {
    const fcn = req.params.fcn;
    
    let args = [];
    if (req.query.args) {
        try {
            args = JSON.parse(req.query.args).map(arg => String(arg));
        } catch (e) {
            args = [String(req.query.args)];
        }
    }

    try {
        if (!globalContract) throw new Error("Contract not initialized.");
        
        const resultBytes = await globalContract.evaluateTransaction(fcn, ...args);
        const result = resultBytes.toString();

        console.log(`[EVALUATE SUCCESS] Function : ${fcn}`);
        res.status(200).json({ result });
    } catch (error) {
        console.error(`[EVALUATE ERROR] ${fcn}: ${error.message}`);
        res.status(500).json({ error: error.message });
    }
});

// ─── 4. HEALTH CHECK ROUTINE ──────────────────────────────────────────────
app.get('/health', (req, res) => {
    const walletOk = fs.existsSync(path.join(__dirname, 'wallet', 'admin.id'));
    res.status(200).json({
        status:    'ok',
        chaincode: chaincodeName,
        channel:   channelName,
        wallet:    walletOk ? 'admin present' : 'MISSING',
        connected: globalContract !== null
    });
});

// ─── 5. BOOTSTRAP SERVER ──────────────────────────────────────────────────
const PORT = process.env.PORT || 3000;

initFabric().then(() => {
    app.listen(PORT, () => {
        console.log('===================================================');
        console.log(`🚀 SDVN Fabric REST API  — Listening on port ${PORT}`);
        console.log(`   Channel  : ${channelName}`);
        console.log(`   Chaincode: ${chaincodeName}`);
        console.log('   Status   : Ready for Hybrid (Sync/Async) traffic');
        console.log('===================================================');
    });
});