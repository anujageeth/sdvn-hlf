/**
 * app.js (v5 - High-Performance SDVN API Gateway)
 *
 * Optimizations applied for NS-3 Simulation:
 * 1. Strict Singleton Connection: Connects to Fabric ONCE on startup.
 * 2. Fire-and-Forget Invokes: Returns HTTP 202 immediately to NS-3.
 * 3. Evaluate queries mapped correctly for read-only functions.
 */

'use strict';

const express    = require('express');
const bodyParser = require('body-parser');
const { Gateway, Wallets } = require('fabric-network');
const path       = require('path');
const fs         = require('fs');

const app = express();
app.use(bodyParser.json({ limit: '10mb' })); // Increased limit for heavy PQC keys
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
        
        // Connect ONCE and hold the connection open
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

// ─── 2. WRITE OPERATIONS (FIRE AND FORGET) ────────────────────────────────
app.post('/invoke/:fcn', (req, res) => {
    const fcn = req.params.fcn;
    
    // Parse arguments sent from NS-3 C++ curl requests
    let args = req.body.args || req.body;
    if (!Array.isArray(args)) {
        args = Object.values(args).map(arg => String(arg));
    } else {
        args = args.map(arg => String(arg));
    }

    // A. Instantly return 202 Accepted to NS-3 so the simulation does not freeze
    res.status(202).json({ 
        status: 'queued', 
        message: `Transaction ${fcn} accepted for processing.` 
    });

    console.log(`[INVOKE QUEUED] Function : ${fcn} | Args received: ${args.length}`);

    // B. Process the transaction consensus in the background asynchronously
    if (globalContract) {
        globalContract.submitTransaction(fcn, ...args)
            .then(() => {
                console.log(`[INVOKE SUCCESS] ${fcn} securely committed to ledger.`);
            })
            .catch(error => {
                console.error(`[INVOKE ERROR] ${fcn} failed during consensus: ${error.message}`);
            });
    } else {
        console.error(`[INVOKE ERROR] Contract not initialized. Cannot execute ${fcn}.`);
    }
});

// ─── 3. READ OPERATIONS (EVALUATE - FAST) ─────────────────────────────────
app.get('/evaluate/:fcn', async (req, res) => {
    const fcn = req.params.fcn;
    
    // Handle parameters passed in URL (e.g., /evaluate/QueryVehicle?args=["V1"])
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
        
        // Evaluate pulls directly from local peer StateDB (Extremely fast, no consensus wait)
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

// Force Fabric to connect before opening the API to NS-3 traffic
initFabric().then(() => {
    app.listen(PORT, () => {
        console.log('===================================================');
        console.log(`SDVN Fabric REST API  — Listening on port ${PORT}`);
        console.log(`Channel  : ${channelName}`);
        console.log(`Chaincode: ${chaincodeName}`);
        console.log('Status   : Ready for high-throughput NS-3 traffic');
        console.log('===================================================');
    });
});