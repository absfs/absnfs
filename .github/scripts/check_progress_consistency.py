#!/usr/bin/env python3
"""
Check consistency between progress tracking and actual client reports.
"""

import os
import sys
import re
import glob

def get_client_status_from_reports():
    """Get the status of all clients from their reports."""
    client_reports = glob.glob("docs/compatibility/clients/*.md")
    clients = {}
    
    for report in client_reports:
        filename = os.path.basename(report)
        client_name = filename.replace(".md", "")
        
        # Check if the report indicates testing in progress
        with open(report, 'r', encoding='utf-8') as f:
            content = f.read()
            is_wip = "In Progress" in content or "🔄" in content
        
        clients[client_name] = "in_progress" if is_wip else "completed"
    
    return clients

def get_client_status_from_progress():
    """Get the list of clients and their status from the progress tracking page."""
    progress_path = "docs/compatibility/progress.md"
    if not os.path.exists(progress_path):
        print(f"Progress tracking file not found: {progress_path}")
        return {}
    
    with open(progress_path, 'r', encoding='utf-8') as f:
        content = f.read()
    
    # Extract client testing queue
    queue_match = re.search(r'## Client Testing Queue.*?\| Priority \| Client \|.*?\n((?:\|.*\n)+)', content, re.DOTALL)
    if not queue_match:
        print("Could not find client testing queue in progress.md")
        return {}
    
    # Parse the client queue
    queue_table = queue_match.group(1)
    clients = {}

    for line in queue_table.strip().split("\n"):
        if "|" not in line:
            continue

        parts = [p.strip() for p in line.split("|")]
        if len(parts) < 5:
            continue

        client = parts[2]
        # Skip separator rows (e.g., |:---:|------|)
        if re.match(r'^[-:]+$', client):
            continue

        status_cell = parts[4]
        
        # Determine status from the cell
        if "🔄" in status_cell:
            status = "in_progress"
        elif "✅" in status_cell:
            status = "completed"
        else:
            status = "not_started"
        
        clients[client] = status
    
    return clients

def normalize_client_name(name):
    """Normalize client name for matching (e.g., 'Linux Kernel 5.15+' -> 'linux-5.15')."""
    name = name.lower()
    # Remove common suffixes/prefixes
    name = re.sub(r'\s*kernel\s*', '-', name)
    name = re.sub(r'\s*\([^)]*\)', '', name)  # Remove parenthetical notes
    name = re.sub(r'[+]+$', '', name)  # Remove trailing +
    name = re.sub(r'\s+', '-', name)  # Replace spaces with dashes
    name = re.sub(r'-+', '-', name)  # Collapse multiple dashes
    name = name.strip('-')
    return name

def find_matching_client(client_name, progress_clients):
    """Find a matching client in progress_clients for the given client_name."""
    normalized_report = normalize_client_name(client_name)
    for progress_client in progress_clients:
        normalized_progress = normalize_client_name(progress_client)
        # Check if one contains the other or they're equal after normalization
        if normalized_report in normalized_progress or normalized_progress in normalized_report:
            return progress_client
        # Also check direct equality
        if normalized_report == normalized_progress:
            return progress_client
    return None

def check_consistency():
    """Check consistency between progress tracking and actual client reports."""
    report_status = get_client_status_from_reports()
    progress_status = get_client_status_from_progress()

    inconsistencies = []

    # Check for clients in reports but not in progress
    for client in report_status:
        matching_progress_client = find_matching_client(client, progress_status.keys())
        if matching_progress_client is None:
            inconsistencies.append(f"Client '{client}' has a report but is not listed in the progress tracking")
    
    # Check for status inconsistencies
    for client in report_status:
        matching_progress_client = find_matching_client(client, progress_status.keys())
        if matching_progress_client:
            report_state = report_status[client]
            progress_state = progress_status[matching_progress_client]

            if report_state == "completed" and progress_state != "completed":
                inconsistencies.append(f"Client '{client}' is marked as completed in report but not in progress tracking")
            elif report_state == "in_progress" and progress_state != "in_progress":
                inconsistencies.append(f"Client '{client}' is marked as in progress in report but not in progress tracking")
    
    # Report findings
    if inconsistencies:
        print("Found inconsistencies between client reports and progress tracking:")
        for issue in inconsistencies:
            print(f"  - {issue}")
        return False
    else:
        print("No inconsistencies found between client reports and progress tracking.")
        return True

def main():
    """Main function."""
    try:
        if check_consistency():
            return 0
        else:
            return 1
    except Exception as e:
        print(f"Error checking progress consistency: {e}")
        return 1

if __name__ == "__main__":
    sys.exit(main())