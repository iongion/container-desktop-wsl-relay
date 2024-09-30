const { spawn } = require("node:child_process");
const http = require('http');
const axios = require("axios");
const adapter = require("axios/lib/adapters/http"); // This doesn't work in the browser

const UNIX_SOCKET = "/var/run/docker.sock";
const NAMED_PIPE = "\\\\.\\pipe\\container-desktop-wsl-relay-test";

let processExited = false;
let child;

async function pingSocket(namedPipe) {
    const statusConfig = {
        socketPath: namedPipe,
        baseURL: "http://d",
        adapter
    };
    console.debug("Checking status of relay server");
    const driver = axios.create(statusConfig);
    const response = await driver.get("/_ping");
    console.debug("Response received", response.status, response.data);
    return response.status === 200;
}

async function exec_buffered(hostLauncher, commandLine, onChunk, onExit, onError) {
    return await new Promise((resolve, reject) => {
        console.debug("Spawning WSL process:", [hostLauncher, ...commandLine].join(" "));
        child = spawn(hostLauncher, commandLine, {
            shell: false,
            windowsHide: false,
            detached: true,
            stdio: ["inherit", "pipe", "pipe"]
        });
        child.stdout.setEncoding("utf8");
        child.stderr.setEncoding("utf8");
        child.stdout.on("data", function (data) {
            const chunk = Buffer.from(data);
            onChunk?.(chunk);
        });
        child.stderr.on("data", function (data) {
            const chunk = Buffer.from(data);
            onChunk?.(chunk);
        });
        child.on("exit", function (code) {
            console.debug("Child process exited", code);
            try {
                if (child.stdin) {
                    console.debug("Ending child process stdin");
                    child.stdin.end();
                }
            } catch (error) {
                console.error("child process stdin end error", error);
            }
            onExit?.({
                //
                pid: child.pid,
                exitCode: child.exitCode,
                command: [hostLauncher, ...commandLine].join(" ")
            });
        });
        child.on("error", function (error) {
            console.error("child process error", error);
            onError?.(error)
        });
        resolve({
            //
            pid: child.pid,
            exitCode: child.exitCode,
            command: [hostLauncher, ...commandLine].join(" ")
        });
    });
}



async function main() {
    // Spawn
    const relayProgramPath = "./container-desktop-wsl-relay.exe"; // If spawned from windows, this path must be converted using wslpath
    try {
        const spawned = await exec_buffered(
            // WSL session with default distribution
            "wsl.exe",
            [
                "--exec",
                // Relay program
                "./container-desktop-wsl-relay",
                `--socket=${UNIX_SOCKET}`,
                `--pipe=${NAMED_PIPE}`,
                `--relay-program-path=${relayProgramPath}`,
                "--pid-file", "container-desktop-wsl-relay.pid"
            ],
            // Chunk handler
            (chunk) => {
                console.debug("Chunk received", chunk.toString());
            },
            // Exit handler
            () => {
                // processExited = true;
            }
        );
        console.debug("Relay started", spawned);
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
    } catch (error) {
        console.error("Unable to spawn", error);
    }
}


function signalHandler(signal) {
    console.debug("Received exit signal", signal);
    if (child) {
        try {
            console.debug("Killing child", child.pid);
            // process.kill(child.pid, "SIGTERM");
            child.kill();
            child.unref();
        } catch (error) {
            console.error("Unable to kill child", error);
        }
    } else {
        console.debug("No child to kill");
    }
    process.exit(0);
}
process.on('SIGINT', signalHandler);
process.on('SIGTERM', signalHandler);
process.on('SIGQUIT', signalHandler);

main();
