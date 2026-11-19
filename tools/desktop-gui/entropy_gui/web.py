"""Derive a browser homepage from a utility endpoint, without opening or probing it."""
import ipaddress
import re
import unicodedata
from urllib.parse import urlsplit


def web_application_url(value):
    """Return the HTTP(S) origin of a complete HTTP(S)/WebSocket endpoint.

    Credentials and malformed authorities are rejected rather than silently
    repairing them. Paths, queries and fragments never reach the browser URL.
    This function does not resolve hosts, connect or launch an application.
    """
    if not isinstance(value, str) or not value or len(value) > 2048:
        raise ValueError("Enter a complete endpoint URL of at most 2048 characters")
    if any(character.isspace() or unicodedata.category(character).startswith("C")
           or character == "\\" for character in value):
        raise ValueError("Endpoint URLs must not contain whitespace, control characters or backslashes")
    try:
        parts = urlsplit(value)
        scheme = {"http": "http", "https": "https", "ws": "http", "wss": "https"}[parts.scheme]
        host = parts.hostname
        port = parts.port
    except (KeyError, ValueError) as error:
        raise ValueError("Use a valid http, https, ws or wss endpoint URL") from error
    if not host or "@" in parts.netloc or parts.netloc.endswith(":") or (port is not None and port < 1):
        raise ValueError("Endpoint URLs require a host and valid port without embedded credentials")
    try:
        address = ipaddress.ip_address(host)
    except ValueError:
        # Refuse alternate numeric IPv4 spellings that browsers can reinterpret
        # (127.1, decimal integer hosts, hexadecimal/octal components).
        if re.fullmatch(r"(?:[0-9]+|0[xX][0-9a-fA-F]+)", host.removesuffix(".").split(".")[-1]):
            raise ValueError("Use a conventional dotted IPv4 address or DNS name") from None
        try:
            normalized = host.encode("idna").decode("ascii").lower()
        except UnicodeError as error:
            raise ValueError("The endpoint DNS name is invalid") from error
        labels = normalized.removesuffix(".").split(".")
        if len(normalized.removesuffix(".")) > 253 or any(
                re.fullmatch(r"[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?", label) is None for label in labels):
            raise ValueError("The endpoint DNS name is invalid")
        authority = normalized
    else:
        if address.is_unspecified or "%" in host:
            raise ValueError("Use a specific host or loopback address for the web application")
        if address.version == 6 and re.fullmatch(r"\[[0-9a-fA-F:.]+\](?::[0-9]+)?", parts.netloc) is None:
            raise ValueError("IPv6 endpoint addresses require brackets and a valid optional port")
        authority = "[" + address.compressed + "]" if address.version == 6 else str(address)
    if port is not None:
        authority += ":" + str(port)
    return scheme + "://" + authority + "/"
