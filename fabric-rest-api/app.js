/**
 * app.js  (v4 — fixes gateway.disconnect() TypeError)
 *
 * Root cause: fabric-network v2.x gateway.disconnect() returns void, not
 * a Promise. Calling .catch() on void throws:
 *   TypeError: Cannot read properties of undefined (reading 'catch')
 *
 * Fix: wrap the disconnect in try/catch instead of chaining .catch().
 */

'use strict';

const express    = require('express');
const bodyParser = require('body-parser');
const { Gateway, Wallets } = require('fabric-network');
const path       = require('path');
const fs         = require('fs');

const app = express();
app.use(bodyParser.json());

const channelName   = 'mychannel';
const chaincodeName = 'sdvncc';

// ─── Build a connected Gateway + Contract for a single call ──────────────────
async function buildGateway() {
    const ccpPath = path.resolve(
        __dirname, '..', 'test-network', 'organizations',
        'peerOrganizations', 'org1.example.com', 'connection-org1.json'
    );
    const ccp    = JSON.parse(fs.readFileSync(ccpPath, 'utf8'));
    const wallet = await Wallets.newFileSystemWallet(path.join(__dirname, 'wallet'));

    const identity = await wallet.get('admin');
    if (!identity) {
        throw new Error(
            'Admin identity not found in wallet. Run: node enrollAdmin.js'
        );
    }

    const gateway = new Gateway();
    await gateway.connect(ccp, {
        wallet,
        identity:  'admin',
        discovery: { enabled: true, asLocalhost: true }
    });

    const network  = await gateway.getNetwork(channelName);
    const contract = network.getContract(chaincodeName);
    return { gateway, contract };
}

// ─── Safe disconnect helper — works whether disconnect() returns void or Promise ──
function safeDisconnect(gateway) {
    if (!gateway) return;
    try {
        const result = gateway.disconnect();
        // If it happens to return a Promise (older versions), swallow rejection
        if (result && typeof result.catch === 'function') {
            result.catch(() => {});
        }
    } catch (_) {
        // void return or synchronous error — both are fine, just swallow
    }
}

// ─── POST /invoke/:fcn  — state-changing (submitTransaction) ─────────────────
app.post('/invoke/:fcn', async (req, res) => {
    const fcn  = req.params.fcn;
    const args = (req.body && Array.isArray(req.body.args)) ? req.body.args : [];

    console.log('\n=========================================');
    console.log(`[INVOKE] Function : ${fcn}`);
    console.log(`[INVOKE] Arguments: ${JSON.stringify(args)}`);

    let gateway = null;
    let contract = null;

    try {
        ({ gateway, contract } = await buildGateway());

        const resultBytes = await contract.submitTransaction(fcn, ...args);
        const result      = resultBytes.toString();

        console.log(`[INVOKE] SUCCESS`);
        console.log(`[INVOKE] Result  : ${result || '(empty)'}`);
        console.log('=========================================\n');

        res.status(200).json({ result });

    } catch (error) {
        console.error(`[INVOKE ERROR] ${fcn}: ${error.message}`);
        console.log('=========================================\n');
        res.status(500).json({ error: error.message });

    } finally {
        safeDisconnect(gateway);   // ← FIX: no more .catch() on void
    }
});

// ─── POST /evaluate/:fcn  — read-only (evaluateTransaction) ──────────────────
app.post('/evaluate/:fcn', async (req, res) => {
    const fcn  = req.params.fcn;
    const args = (req.body && Array.isArray(req.body.args)) ? req.body.args : [];

    console.log('\n-----------------------------------------');
    console.log(`[EVALUATE] Function : ${fcn}`);
    console.log(`[EVALUATE] Arguments: ${JSON.stringify(args)}`);

    let gateway = null;
    let contract = null;

    try {
        ({ gateway, contract } = await buildGateway());

        const resultBytes = await contract.evaluateTransaction(fcn, ...args);
        const result      = resultBytes.toString();

        console.log(`[EVALUATE] Result: ${result}`);
        console.log('-----------------------------------------\n');

        res.status(200).json({ result });

    } catch (error) {
        console.error(`[EVALUATE ERROR] ${fcn}: ${error.message}`);
        console.log('-----------------------------------------\n');
        res.status(500).json({ error: error.message });

    } finally {
        safeDisconnect(gateway);   // ← FIX
    }
});

// ─── GET /health ──────────────────────────────────────────────────────────────
app.get('/health', async (req, res) => {
    const walletOk = fs.existsSync(path.join(__dirname, 'wallet', 'admin.id'));
    res.status(200).json({
        status:    'ok',
        chaincode: chaincodeName,
        channel:   channelName,
        wallet:    walletOk ? 'admin present' : 'MISSING — run node enrollAdmin.js'
    });
});

app.listen(3000, () => {
    console.log('===========================================');
    console.log('  SDVN Fabric REST API  — port 3000');
    console.log(`  Channel  : ${channelName}`);
    console.log(`  Chaincode: ${chaincodeName}`);
    console.log('  Routes:');
    console.log('    POST /invoke/:fcn   — submitTransaction');
    console.log('    POST /evaluate/:fcn — evaluateTransaction');
    console.log('    GET  /health        — liveness + wallet check');
    console.log('===========================================\n');
});
