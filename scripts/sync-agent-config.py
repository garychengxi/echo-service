#!/usr/bin/env python3
"""Additive, explicit configuration sync through Echo Service; Python 3.10+."""
import argparse
import ipaddress
import json
import os
from pathlib import Path
import sys
from urllib.error import HTTPError, URLError
from urllib.parse import quote, urlsplit
from urllib.request import HTTPRedirectHandler, Request, build_opener

MAX_FILE_BYTES = 1_000_000


def validate_service_url(value):
    parsed = urlsplit(value)
    try:
        loopback = ipaddress.ip_address(parsed.hostname or "").is_loopback
    except ValueError:
        loopback = parsed.hostname == "localhost"
    if (parsed.scheme not in ("https", "http") or not parsed.hostname
            or parsed.username is not None or parsed.password is not None
            or parsed.query or parsed.fragment
            or (parsed.scheme == "http" and not loopback)
            or parsed.path.rstrip("/") != "/api/v1"):
        raise ValueError("service URL must be HTTPS /api/v1 (HTTP is allowed only on loopback)")
    return value.rstrip("/")


def unique_object(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError("duplicate JSON object key")
        result[key] = value
    return result


def read_json(path):
    try:
        with Path(path).open("rb") as source:
            raw = source.read(MAX_FILE_BYTES + 1)
        if len(raw) > MAX_FILE_BYTES:
            raise ValueError("input exceeds 1 MB")
        return json.loads(raw.decode("utf-8-sig"), object_pairs_hook=unique_object)
    except (OSError, UnicodeError, json.JSONDecodeError) as from_error:
        raise ValueError("cannot read a valid UTF-8 JSON input file") from from_error


def valid_name(name):
    return (isinstance(name, str) and 1 <= len(name) <= 64
            and name[0].isalnum()
            and all(char.isalnum() or char in "_-" for char in name))


def load_skills(path):
    if not path:
        return []
    value = read_json(path)
    if not isinstance(value, list):
        raise ValueError("skills input must be an array")
    seen = set()
    for skill in value:
        if (not isinstance(skill, dict) or set(skill) != {"name", "description", "instruction"}
                or not valid_name(skill["name"])
                or not isinstance(skill["description"], str) or not skill["description"].strip()
                or len(skill["description"]) > 200
                or not isinstance(skill["instruction"], str) or not skill["instruction"].strip()
                or len(skill["instruction"]) > 80_000
                or skill["name"] in seen):
            raise ValueError("invalid skill fields, name or duplicate name")
        seen.add(skill["name"])
    return value


def valid_mcp(config):
    if not isinstance(config, dict):
        return False
    transport = config.get("transport", config.get("type"))
    if transport is None:
        transport = "stdio" if "command" in config else "http" if "url" in config else None
    if transport == "stdio":
        if not isinstance(config.get("command"), str) or not config["command"].strip():
            return False
        if "args" in config and (not isinstance(config["args"], list) or not all(isinstance(arg, str) for arg in config["args"])):
            return False
    elif transport in ("http", "sse"):
        if not isinstance(config.get("url"), str):
            return False
        url = urlsplit(config["url"])
        if url.scheme not in ("http", "https") or not url.hostname or url.username is not None:
            return False
    else:
        return False
    for field in ("env", "headers"):
        if field in config and (not isinstance(config[field], dict) or not all(isinstance(v, str) for v in config[field].values())):
            return False
    return True


def load_mcp(path):
    if not path:
        return {}
    value = read_json(path)
    if not isinstance(value, dict) or set(value) != {"mcpServers"} or not isinstance(value["mcpServers"], dict):
        raise ValueError("MCP input must contain only a mcpServers object")
    for key, config in value["mcpServers"].items():
        if (not key or len(key.encode("utf-8")) > 200 or key != key.strip()
                or any(char in key for char in "/\\\x00\r\n") or not valid_mcp(config)):
            raise ValueError("invalid MCP key or configuration")
    return value["mcpServers"]


class NoRedirect(HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


class Service:
    def __init__(self, base, token):
        self.base = validate_service_url(base)
        self.token = token
        self.opener = build_opener(NoRedirect())

    def request(self, method, path, body=None):
        encoded = None if body is None else json.dumps(body, ensure_ascii=False).encode("utf-8")
        if encoded is not None and len(encoded) > MAX_FILE_BYTES:
            raise ValueError("encoded request exceeds Service body limit")
        request = Request(self.base + path, data=encoded, method=method, headers={
            "Authorization": "Bearer " + self.token, "Content-Type": "application/json",
        })
        try:
            with self.opener.open(request, timeout=45) as response:
                raw = response.read(8 * MAX_FILE_BYTES + 1)
                if len(raw) > 8 * MAX_FILE_BYTES:
                    raise ValueError("Service response exceeds limit")
                envelope = json.loads(raw)
                if (not isinstance(envelope, dict) or not isinstance(envelope.get("error"), dict)
                        or envelope["error"].get("code") != "" or envelope["error"].get("statusCode") != 0):
                    raise ValueError("invalid Service response")
                return envelope.get("data")
        except HTTPError as error:
            error.close()
            raise ValueError("Service HTTP " + str(error.code) + "; mutation outcome may be unknown; reconcile before retry") from None
        except (URLError, TimeoutError, OSError, json.JSONDecodeError):
            raise ValueError("Service request failed; mutation outcome may be unknown; reconcile before retry") from None


def synchronize(service, container, skills, servers):
    root = "/containers/" + quote(container, safe="")
    existing = {}
    if skills:
        catalog = service.request("GET", root + "/skills?scope=user")
        if not isinstance(catalog, dict) or not isinstance(catalog.get("skills"), list):
            raise ValueError("invalid skill catalog; no sync was started")
        for skill in catalog["skills"]:
            if not isinstance(skill, dict) or not isinstance(skill.get("name"), str) or not isinstance(skill.get("scope"), str):
                raise ValueError("invalid skill catalog; no sync was started")
            if skill["scope"] != "user":
                continue
            if skill["name"] in existing:
                raise ValueError("ambiguous user skills; no sync was started")
            existing[skill["name"]] = skill
    for skill in skills:
        name = skill["name"]
        if name in existing:
            service.request("PUT", root + "/skills/" + quote(name, safe=""),
                            {"description": skill["description"], "instruction": skill["instruction"]})
        else:
            service.request("POST", root + "/skills", skill)
        print("applied skill " + json.dumps(name, ensure_ascii=True))
    for key, config in servers.items():
        service.request("POST", root + "/mcp-servers", {"config": json.dumps({"mcpServers": {key: config}}, ensure_ascii=False)})
        print("applied MCP " + json.dumps(key, ensure_ascii=True))


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--service-url", required=True, help="Service base, e.g. https://echo.example/api/v1")
    parser.add_argument("--container-id", required=True, help="Explicit assigned container ID")
    parser.add_argument("--skills", help="JSON array of name/description/instruction objects")
    parser.add_argument("--mcp", help="JSON object containing mcpServers")
    parser.add_argument("--apply", action="store_true", help="Apply additive updates; default is offline dry-run")
    args = parser.parse_args(argv)
    try:
        base = validate_service_url(args.service_url)
        if (not args.container_id or len(args.container_id) > 256
                or any(char in args.container_id for char in "/\\\x00\r\n")):
            raise ValueError("invalid container ID")
        if not args.skills and not args.mcp:
            raise ValueError("supply --skills and/or --mcp")
        # Parse and validate the entire desired input before any network side effect.
        skills, servers = load_skills(args.skills), load_mcp(args.mcp)
        # Account for JSON escaping and wrapper overhead before starting any mutation.
        for payload in [*skills, *({"config": json.dumps({"mcpServers": {k: v}}, ensure_ascii=False)} for k, v in servers.items())]:
            if len(json.dumps(payload, ensure_ascii=False).encode("utf-8")) > MAX_FILE_BYTES:
                raise ValueError("an encoded request exceeds Service body limit")
        if not args.apply:
            print("dry-run: " + str(len(skills)) + " skill(s), " + str(len(servers)) + " MCP(s); no network calls, deletions or changes")
            return 0
        token = os.environ.get("ECHO_SERVICE_ACCESS_TOKEN", "")
        if not token or "\r" in token or "\n" in token:
            raise ValueError("set ECHO_SERVICE_ACCESS_TOKEN to a valid access token")
        synchronize(Service(base, token), args.container_id, skills, servers)
        print("sync complete; unrelated resources were not removed")
        return 0
    except (ValueError, TypeError, KeyError) as error:
        # Errors from the HTTP boundary deliberately contain no response/body/credentials.
        print("sync stopped: " + str(error) + ". Prior applied resources remain; reconcile before retry.", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
