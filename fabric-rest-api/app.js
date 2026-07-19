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
        'SeedEndorserSet'
    ];

    if (syncFunctions.includes(fcn)) {
        // =================================================================
        // SYNCHRONOUS MODE: Block NS-3 until the ledger is fully updated
        // =================================================================
        console.log(`[INVOKE SYNC] Processing : ${fcn} | Waiting for consensus...`);
        try {
            await globalContract.submitTransaction(fcn, ...args);
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

        // Process in background
        globalContract.submitTransaction(fcn, ...args)
            .then(() => {
                // Optional: Mute success logs for hashes if it clutters your terminal
                // console.log(`[ASYNC SUCCESS] ${fcn} committed.`);
            })
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