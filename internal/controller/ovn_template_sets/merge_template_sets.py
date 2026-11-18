#!/usr/bin/env python3
# Copyright 2026 Illumio, Inc. All Rights Reserved.
"""Merge the IPFIX template records in a packet capture into one IPFIX message.

Open vSwitch limits each IPFIX message to about 1024 bytes, so it exports its
templates as several messages. cloud-operator preloads a single message at
startup, so this tool merges the template records of all the captured messages
into one template set in one message.

usage: merge_template_sets.py capture.pcap openvswitch.bin
"""
import struct
import sys

IPFIX_VERSION = 10
TEMPLATE_SET_ID = 2
LINK_HEADER_LENGTHS = {1: 14, 113: 16, 276: 20}  # Ethernet, Linux SLL, Linux SLL2


def udp_payloads(pcap):
    """Yield the UDP payload of each packet in a little-endian pcap file."""
    if struct.unpack("<I", pcap[:4])[0] not in (0xA1B2C3D4, 0xA1B23C4D):
        raise SystemExit("not a little-endian pcap file")
    link_type = struct.unpack("<I", pcap[20:24])[0]
    if link_type not in LINK_HEADER_LENGTHS:
        raise SystemExit(f"unsupported link type {link_type}")
    offset = 24
    while offset + 16 <= len(pcap):
        captured_length = struct.unpack("<I", pcap[offset + 8:offset + 12])[0]
        offset += 16
        ip = pcap[offset + LINK_HEADER_LENGTHS[link_type]:offset + captured_length]
        offset += captured_length
        if len(ip) < 28:
            continue
        version = ip[0] >> 4
        if version == 4 and ip[9] == 17:
            yield ip[(ip[0] & 0xF) * 4 + 8:]
        elif version == 6 and ip[6] == 17:
            yield ip[48:]


def template_records(message):
    """Yield (observation domain ID, template ID, record bytes) for each template record."""
    if len(message) < 16:
        return
    version, length, _, _, domain = struct.unpack("!HHIII", message[:16])
    if version != IPFIX_VERSION:
        return
    position, end = 16, min(length, len(message))
    while position + 4 <= end:
        set_id, set_length = struct.unpack("!HH", message[position:position + 4])
        if set_length < 4:
            break
        if set_id == TEMPLATE_SET_ID:
            cursor, set_end = position + 4, position + set_length
            while cursor + 4 <= set_end:
                start = cursor
                template_id, field_count = struct.unpack("!HH", message[cursor:cursor + 4])
                cursor += 4
                if template_id < 256:
                    break  # padding
                for _ in range(field_count):
                    field_id = struct.unpack("!H", message[cursor:cursor + 2])[0]
                    cursor += 8 if field_id & 0x8000 else 4  # enterprise fields are longer
                if cursor > set_end:
                    break
                yield domain, template_id, message[start:cursor]
        position += set_length


def main():
    if len(sys.argv) != 3:
        raise SystemExit(__doc__)
    records, domains = {}, set()
    with open(sys.argv[1], "rb") as capture:
        for payload in udp_payloads(capture.read()):
            for domain, template_id, record in template_records(payload):
                domains.add(domain)
                if records.setdefault(template_id, record) != record:
                    raise SystemExit(f"template {template_id} has conflicting definitions")
    if len(domains) != 1:
        raise SystemExit(f"expected one observation domain, found {sorted(domains)}")
    body = b"".join(records[template_id] for template_id in sorted(records))
    template_set = struct.pack("!HH", TEMPLATE_SET_ID, 4 + len(body)) + body
    message = struct.pack("!HHIII", IPFIX_VERSION, 16 + len(template_set), 0, 0, domains.pop()) + template_set
    if len(message) > 0xFFFF:
        raise SystemExit("the merged templates do not fit in one IPFIX message")
    with open(sys.argv[2], "wb") as output:
        output.write(message)
    print(f"merged {len(records)} templates into {sys.argv[2]} ({len(message)} bytes)")


if __name__ == "__main__":
    main()
