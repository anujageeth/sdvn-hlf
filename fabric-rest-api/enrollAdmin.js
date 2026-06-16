/**
 * enrollAdmin.js
 *
 * Run this script ONCE after every `./network.sh up` to enroll the Org1 admin
 * against the freshly-started Fabric CA and store the identity in the wallet.
 *
 * Usage:
 *   cd /home/anujageeth/hyperledger_fabric/fabric-samples/fabric-rest-api
 *   node enrollAdmin.js
 */

'use strict';

const { Wallets }        = require('fabric-network');
const FabricCAServices   = require('fabric-ca-client');
const path               = require('path');
const fs                 = require('fs');

async function main() {
    try {
        // ── 1. Load the Org1 connection profile ───────────────────────────────
        const ccpPath = path.resolve(
            __dirname, '..', 'test-network', 'organizations',
            'peerOrganizations', 'org1.example.com', 'connection-org1.json'
        );

        if (!fs.existsSync(ccpPath)) {
            throw new Error(
                `Connection profile not found at:\n  ${ccpPath}\n` +
                `Make sure you have run: ./network.sh up createChannel -c mychannel -ca`
            );
        }

        const ccp = JSON.parse(fs.readFileSync(ccpPath, 'utf8'));
        console.log('Connection profile loaded from:\n ', ccpPath);

        // ── 2. Build a Fabric CA client ───────────────────────────────────────
        const caInfo     = ccp.certificateAuthorities['ca.org1.example.com'];
        const caTLSCerts = caInfo.tlsCACerts.pem;

        const ca = new FabricCAServices(
            caInfo.url,
            { trustedRoots: caTLSCerts, verify: false },
            caInfo.caName
        );
        console.log(`CA URL  : ${caInfo.url}`);
        console.log(`CA name : ${caInfo.caName}`);

        // ── 3. Open the wallet ────────────────────────────────────────────────
        const walletPath = path.join(__dirname, 'wallet');
        const wallet     = await Wallets.newFileSystemWallet(walletPath);
        console.log(`Wallet  : ${walletPath}`);

        // ── 4. Enroll (always overwrite — network may have been restarted) ───
        const existing = await wallet.get('admin');
        if (existing) {
            console.log('Removing stale admin identity from wallet...');
            await wallet.remove('admin');
        }

        console.log('Enrolling admin with CA (ID: admin / secret: adminpw)...');
        const enrollment = await ca.enroll({
            enrollmentID:     'admin',
            enrollmentSecret: 'adminpw'
        });

        const x509Identity = {
            credentials: {
                certificate: enrollment.certificate,
                privateKey:  enrollment.key.toBytes(),
            },
            mspId: 'Org1MSP',
            type:  'X.509',
        };

        await wallet.put('admin', x509Identity);

        console.log('\n✓ Admin enrolled and stored in wallet.');
        console.log('  You can now start app.js: node app.js\n');

    } catch (error) {
        console.error('\n✗ enrollAdmin.js failed:', error.message);
        process.exit(1);
    }
}

main();
