#!/usr/bin/env python3
"""Send a deterministic counter to the source lab; run on entropy-server's host.

TCP listens for entropy-server to connect. UDP sends to entropy-server's bound
listener after Start. This is demonstration data, not a random/entropy source.
"""
import argparse
import socket
import time


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument("protocol",choices=["tcp","udp"])
    parser.add_argument("--host",default="127.0.0.1",help="TCP bind address or UDP destination on the server host")
    parser.add_argument("--port",type=int,help="default: TCP 9001 or UDP 9002")
    parser.add_argument("--bytes",type=int,default=512,help="bytes per write/datagram (default: 512)")
    parser.add_argument("--interval",type=float,default=.25,help="seconds between writes (default: .25)")
    args=parser.parse_args();port=args.port if args.port is not None else 9001 if args.protocol=="tcp" else 9002
    if not 1<=port<=65535 or not 1<=args.bytes<=65507 or not .02<=args.interval<=60:parser.error("port: 1..65535; bytes: 1..65507; interval: .02..60 seconds")
    payload=(bytes(range(256))*((args.bytes+255)//256))[:args.bytes]
    family=socket.AF_INET6 if ":" in args.host else socket.AF_INET
    try:
        if args.protocol=="tcp":
            with socket.create_server((args.host,port),family=family) as listener:
                print(f"TCP counter producer listening on {args.host}:{port}; waiting for entropy-server",flush=True)
                while True:
                    conn,_=listener.accept()
                    with conn:
                        print("TCP client connected; sending counter bytes",flush=True)
                        try:
                            while True:conn.sendall(payload);time.sleep(args.interval)
                        except (BrokenPipeError,ConnectionResetError):pass
        else:
            with socket.socket(family,socket.SOCK_DGRAM) as sender:
                print(f"UDP counter sender to {args.host}:{port}; Start the registered listener first",flush=True)
                while True:sender.sendto(payload,(args.host,port));time.sleep(args.interval)
    except KeyboardInterrupt:pass


if __name__=="__main__":main()
