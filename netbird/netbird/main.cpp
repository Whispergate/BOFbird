#include "beacon.h"
#include <stdexcept>
#include <string_view>
#include <ranges>
#include <optional>
#include <utils.h>
#include <jsoncons/json.hpp>
#include <winternl.h>

using namespace jsoncons;

const char RequestTemplate[] = R"(%s /localapi/v0/%s HTTP/1.0
Connection: close
Host: local-netbirdd.sock
User-Agent: BOFbird
Content-Type: application/json
Content-Length: %d

)";

struct HttpStatusLine {
    std::string version;
    int statusCode;
    std::string reasonPhrase;
};

enum HttpStatus {
    Ok = 200,
    NoContent = 204
};


HANDLE OpenPipe(const std::string &path) {
	return CreateFile(path.c_str(), GENERIC_READ | GENERIC_WRITE, 0, nullptr, OPEN_EXISTING, SECURITY_SQOS_PRESENT | SECURITY_IMPERSONATION, nullptr);
}

// Helper to read exactly N bytes from pipe
bool ReadExact(HANDLE pipe, void* buffer, DWORD bytesToRead) {
    BYTE* ptr = static_cast<BYTE*>(buffer);
    while (bytesToRead > 0) {
        DWORD chunk = 0;
        if (!ReadFile(pipe, ptr, bytesToRead, &chunk, nullptr)) {
            return false; // error
        }
        if (chunk == 0) break; // EOF
        ptr += chunk;
        bytesToRead -= chunk;
    }
    return true;
}

// Read headers into a string
std::string ReadHeaders(HANDLE pipe) {
    std::string headers;
    char c;
    int consecutiveCRLF = 0;

    while (true) {
        DWORD read = 0;
        if (!ReadFile(pipe, &c, 1, &read, nullptr) || read == 0) {
            break;
        }
        headers.push_back(c);

        // Track CRLF sequence
        if (c == '\n') {
            consecutiveCRLF++;
            if (consecutiveCRLF >= 2) break; // empty line = end of headers
        }
        else if (c != '\r') {
            consecutiveCRLF = 0;
        }
    }
    return headers;
}

// Extract Content-Length if present
size_t ParseContentLength(const std::string& headers) {
    std::istringstream ss(headers);
    std::string line;
    while (std::getline(ss, line)) {
        if (line.find("Content-Length:") != std::string::npos) {
            size_t pos = line.find(":");
            if (pos != std::string::npos) {
                return std::stoul(line.substr(pos + 1));
            }
        }
    }
    return 0;
}


HttpStatusLine ParseStatusLine(const std::string& headers) {
    HttpStatusLine result;
    std::istringstream iss(headers);

    iss >> result.version >> result.statusCode;
    std::getline(iss, result.reasonPhrase);

    // Trim leading space from reasonPhrase
    if (!result.reasonPhrase.empty() && result.reasonPhrase[0] == ' ')
        result.reasonPhrase.erase(0, 1);

    return result;
}


json ReadFully(HANDLE pipe, HttpStatusLine& statusLine) {

    std::string headers = ReadHeaders(pipe);
    size_t contentLength = ParseContentLength(headers);
    statusLine = ParseStatusLine(headers);

    std::string body;

    if (contentLength > 0) {
        body.resize(contentLength);
        if (!ReadExact(pipe, body.data(), (DWORD)contentLength)) {
            throw std::runtime_error("Failed to read full HTTP body with Content-Length");
        }
    }
    else {
        // No Content-Length: read until pipe closes
        char buffer[4096];
        DWORD bytesRead = 0;
        while (ReadFile(pipe, buffer, sizeof(buffer), &bytesRead, nullptr) && bytesRead > 0) {
            body.append(buffer, bytesRead);
        }
    }

    if (!body.empty())
        return json::parse(body);
    else
        return json::null();
}


json SendCommand(const std::string& pipe, const std::string_view& method, const std::string_view& path, const json& body, int expectedStatusCode) {

	auto written = 0ul;
	auto hPipe = OpenPipe(pipe);
	auto err = GetLastError();
    auto body_data = body.to_string();
    auto contentLength = body.empty() ? 0 : body_data.size();
    HttpStatusLine status;
    char buffer[1024];

	if (hPipe == INVALID_HANDLE_VALUE) {
		throw std::runtime_error("Failed to connect to netbirdd IPC pipe, is netbirdd async BOF running and using the same pipe?");
	}
	scoped_exit close([hPipe] {
		CloseHandle(hPipe);
	});

	auto size = sprintf_s(buffer, sizeof(buffer), RequestTemplate, method.data(), path.data(), contentLength);

	if (!WriteFile(hPipe, buffer, size, &written, nullptr)) {
		throw std::runtime_error("Failed to write headers to pipe");
	}

    if (contentLength > 0) {
        if (!WriteFile(hPipe, body_data.c_str(), body_data.size(), &written, nullptr)) {
            throw std::runtime_error("Failed to write body to pipe");
        }
    }

	auto result = ReadFully(hPipe, status);

    if(status.statusCode != expectedStatusCode)
        throw std::runtime_error("Unexpected HTTP status code");

    return result;
}

static void PrintPeer(const json& peer) {
    std::string status_line = "";

    auto connStatus = peer.contains("ConnStatus") ? peer["ConnStatus"].as_string() : "";
    auto ip = peer.contains("IP") ? peer["IP"].as_string() : "";
    auto fqdn = peer.contains("FQDN") ? peer["FQDN"].as_string() : "";
    auto relayed = peer.contains("Relayed") ? peer["Relayed"].as_bool() : false;

    if (connStatus == "Connected") {
        if (relayed) {
            status_line = "connected, relayed";
        } else {
            status_line = "connected, direct";
        }
    } else {
        status_line = connStatus;
    }

    BeaconPrintf(CALLBACK_OUTPUT, "%-20s %-40s %-20s\n", ip.c_str(), fqdn.c_str(), status_line.c_str());
}

static json GetStatus(const std::string& pipe) {
    return SendCommand(pipe, "GET", "status", json(), HttpStatus::Ok);
}

std::map<std::string, std::string> GetBeaconArgs(datap* data) {

    std::map<std::string, std::string> args;
    std::string token;
    char* ptr;

    while ( (ptr = BeaconDataExtract(data, nullptr)) != nullptr) {
        token = std::string(ptr);
        if (token.rfind("--", 0) != 0) {
            if(!args.contains("command"))
                args["command"] = token;
            else
                BeaconPrintf(CALLBACK_OUTPUT, "Warning: multiple command found within argument array, ignoring: %s\n", token.c_str());
            
            continue;
        }

        token = token.substr(2); 

        std::string key, value;
        size_t eq = token.find('=');

        if (eq != std::string::npos) {
            key = token.substr(0, eq);
            value = token.substr(eq + 1);
        }
        else {
            key = token;
            ptr = BeaconDataExtract(data, nullptr);
            std::string next = ptr ? std::string(ptr) : "";
            if (!next.empty() && next.rfind("--", 0) != 0) {
                value = next;
            }
            else {
                value = "true";
                if (!next.empty())
                    BeaconPrintf(CALLBACK_OUTPUT, "Warning: unexpected token after flag: %s\n", next.c_str());
            }
        }

        args[key] = value;
    }

    return args;
}


extern "C" __declspec(dllexport) void go(const char* data, int len) {

	try {

		datap args;
		json body;
        json status;

        if (data == nullptr || len == 0) {
            BeaconPrintf(CALLBACK_OUTPUT, "No arguments provided\n");
            return;
        }

		BeaconDataParse(&args, (char*)data, len);
        auto parsedArgs = GetBeaconArgs(&args);        
        const auto& command = parsedArgs["command"];
        auto pipe = std::string();

        if (parsedArgs.contains("socket"))
            pipe = parsedArgs["socket"];
        else {
            BeaconPrintf(CALLBACK_OUTPUT, "[!] No socket provided, bailing\n");
            return;
        }
        
		auto size = 0ul;        
		auto path = std::string();

        if (!pipe.empty()) {
            status = GetStatus(pipe.c_str());
            BeaconPrintf(CALLBACK_OUTPUT, "[=] Fetched latest status\n");
        }       
		
		if (command == "status") {
			            
            if (!status.empty()) {
                if (parsedArgs.contains("json")) {
                    std::ostringstream oss;
                    oss << jsoncons::pretty_print(status);
                    BeaconPrintf(CALLBACK_OUTPUT, "%s", oss.str().c_str());
                }
                else {
                    // Print management state
                    if (status.contains("ManagementState")) {
                        auto& mgmt = status["ManagementState"];
                        auto connected = mgmt.contains("Connected") ? mgmt["Connected"].as_bool() : false;
                        auto url = mgmt.contains("URL") ? mgmt["URL"].as_string() : "";
                        BeaconPrintf(CALLBACK_OUTPUT, "Management: %s (%s)\n", url.c_str(), connected ? "connected" : "disconnected");
                    }

                    // Print signal state
                    if (status.contains("SignalState")) {
                        auto& sig = status["SignalState"];
                        auto connected = sig.contains("Connected") ? sig["Connected"].as_bool() : false;
                        auto url = sig.contains("URL") ? sig["URL"].as_string() : "";
                        BeaconPrintf(CALLBACK_OUTPUT, "Signal: %s (%s)\n", url.c_str(), connected ? "connected" : "disconnected");
                    }

                    // Print local peer info
                    if (status.contains("LocalPeerState")) {
                        auto& local = status["LocalPeerState"];
                        auto ip = local.contains("IP") ? local["IP"].as_string() : "";
                        auto fqdn = local.contains("FQDN") ? local["FQDN"].as_string() : "";
                        auto pubKey = local.contains("PubKey") ? local["PubKey"].as_string() : "";
                        BeaconPrintf(CALLBACK_OUTPUT, "\nSelf:\n");
                        BeaconPrintf(CALLBACK_OUTPUT, "  IP:     %s\n", ip.c_str());
                        BeaconPrintf(CALLBACK_OUTPUT, "  FQDN:   %s\n", fqdn.c_str());
                        BeaconPrintf(CALLBACK_OUTPUT, "  PubKey: %s\n", pubKey.c_str());
                    }

                    // Print peers
                    if (status.contains("Peers") && status["Peers"].is_array() && !status["Peers"].empty()) {
                        BeaconPrintf(CALLBACK_OUTPUT, "\nPeers:\n");
                        BeaconPrintf(CALLBACK_OUTPUT, "%-20s %-40s %-20s\n", "IP", "FQDN", "Status");
                        BeaconPrintf(CALLBACK_OUTPUT, "%-20s %-40s %-20s\n", "----", "----", "------");
                        for (const auto& peer : status["Peers"].array_range()) {
                            PrintPeer(peer);
                        }
                    } else {
                        BeaconPrintf(CALLBACK_OUTPUT, "\nNo peers connected\n");
                    }

                    // Print relays
                    if (status.contains("Relays") && status["Relays"].is_array() && !status["Relays"].empty()) {
                        BeaconPrintf(CALLBACK_OUTPUT, "\nRelays:\n");
                        for (const auto& relay : status["Relays"].array_range()) {
                            auto uri = relay.contains("URI") ? relay["URI"].as_string() : "";
                            auto avail = relay.contains("Available") ? relay["Available"].as_bool() : false;
                            BeaconPrintf(CALLBACK_OUTPUT, "  %s (%s)\n", uri.c_str(), avail ? "available" : "unavailable");
                        }
                    }
                }
            }
            else {
                BeaconPrintf(CALLBACK_OUTPUT, "[!] Failed to get status from backend\n");
            }
		}
		else if (command == "up") {
            // The daemon is already started at boot; "up" is essentially a re-start
            SendCommand(pipe, "POST", "up", json(), HttpStatus::Ok);
            BeaconPrintf(CALLBACK_OUTPUT, "[+] Up command sent\n");
        }
        else if (command == "down") {
            SendCommand(pipe, "POST", "down", json(), HttpStatus::Ok);
            BeaconPrintf(CALLBACK_OUTPUT, "[+] Down command sent\n");
        }
        else if (command == "shutdown") {
            SendCommand(pipe, "POST", "shutdown", json(), HttpStatus::Ok);
            BeaconPrintf(CALLBACK_OUTPUT, "[+] Shutdown command sent\n");
        }
        else {
            BeaconPrintf(CALLBACK_OUTPUT, "[!] Unsupported command %s\n", command.c_str());
        }
	}
	catch (const std::exception& e) {
		BeaconPrintf(CALLBACK_OUTPUT, "[!] Error: %s\n", e.what());	
	}
}

BEACON_MAIN("zzzzzzzzzzz", go)
