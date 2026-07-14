'use strict';

const path = require('path');
const fs = require('fs');
const grpc = require('@grpc/grpc-js');
const protoLoader = require('@grpc/proto-loader');
const { Gateway, Wallets } = require('fabric-network');

const PROTO_PATH = path.join(__dirname, 'proto', 'sdvn_gateway.proto');
const packageDef = protoLoader.loadSync(PROTO_PATH, {
    keepCase: false, longs: String, enums: String, defaults: true, oneofs: true
});
const sdvngw = grpc.loadPackageDefinition(packageDef).sdvngw;

const channelName = 'mychannel';
const chaincodeName = 'sdvncc';

let globalGateway = null;
let globalContract = null;

async function initFabric() {
    const ccpPath = path.resolve(
        __dirname, '..', 'test-network', 'organizations',
        'peerOrganizations', 'org1.example.com', 'connection-org1.json'
    );
    const ccp = JSON.parse(fs.readFileSync(ccpPath, 'utf8'));
    const wallet = await Wallets.newFileSystemWallet(path.join(__dirname, 'wallet'));

    const identity = await wallet.get('admin');
    if (!identity) throw new Error('Admin identity not found. Run enrollAdmin.js first.');

    globalGateway = new Gateway();
    await globalGateway.connect(ccp, {
        wallet, identity: 'admin',
        discovery: { enabled: true, asLocalhost: true }
    });

    const network = await globalGateway.getNetwork(channelName);
    globalContract = network.getContract(chaincodeName);
    console.log('✅ Fabric Gateway connected (persistent).');
}

// ─── RPC handlers ──────────────────────────────────────────────
function invoke(call, callback) {
    const { fcn, args } = call.request;

    // ack immediately — NS-3 does not block on consensus
    callback(null, { status: 'queued', message: `${fcn} accepted` });

    if (!globalContract) {
        console.error(`[INVOKE ERROR] Contract not initialized. Dropped ${fcn}.`);
        return;
    }
    globalContract.submitTransaction(fcn, ...args)
        .then(() => console.log(`[INVOKE OK] ${fcn} committed.`))
        .catch(err => console.error(`[INVOKE FAIL] ${fcn}: ${err.message}`));
}

async function evaluate(call, callback) {
    const { fcn, args } = call.request;
    try {
        if (!globalContract) throw new Error('Contract not initialized.');
        const resultBytes = await globalContract.evaluateTransaction(fcn, ...args);
        callback(null, { result: resultBytes.toString(), error: '' });
    } catch (err) {
        callback(null, { result: '', error: err.message });
    }
}

function health(call, callback) {
    callback(null, { status: 'ok', connected: globalContract !== null });
}

// ─── Bootstrap ─────────────────────────────────────────────────
const server = new grpc.Server();
server.addService(sdvngw.FabricGateway.service, { invoke, evaluate, health });

const PORT = process.env.GRPC_PORT || 50051;

initFabric().then(() => {
    server.bindAsync(`0.0.0.0:${PORT}`, grpc.ServerCredentials.createInsecure(), () => {
        console.log(`SDVN Fabric gRPC gateway listening on ${PORT}`);
    });
}).catch(err => {
    console.error('❌ Fabric init failed:', err.message);
    process.exit(1);
});