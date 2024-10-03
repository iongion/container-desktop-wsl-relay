const axios = require("axios");
const adapter = require("axios/lib/adapters/http"); // This doesn't work in the browser

const UNIX_SOCKET = "/var/run/docker.sock";
const NAMED_PIPE = "\\\\.\\pipe\\container-desktop-test";

let processExited = false;
let child;

async function pingSocket(namedPipe) {
    const statusConfig = {
        socketPath: namedPipe,
        baseURL: "http://d",
        adapter,
        timeout: 60000
    };
    console.debug("Checking status of relay server");
    const driver = axios.create(statusConfig);
    const response = await driver.get("/_ping");
    console.debug("Response received", response.status, response.data);
    return response.status === 200;
}

async function main() {
    // Spawn
    let isRequesting = false;
    const iid = setInterval(async () => {
        if (processExited) {
            console.debug("Child exited");
            clearInterval(iid);
        }
        if (isRequesting) {
            console.debug("A request is already in progress");
            return;
        }
        try {
            console.debug(`Issuing ping request to ${NAMED_PIPE} <==> ${UNIX_SOCKET}`);
            isRequesting = true;
            await pingSocket(NAMED_PIPE);
        } catch (error) {
            console.debug("Request error", error.message);
        } finally {
            isRequesting = false;
        }
    }, 1500)
}

main();
