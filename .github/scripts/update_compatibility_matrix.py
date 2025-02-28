#!/usr/bin/env python3
"""
Automatically update the compatibility matrix in the index.md file based on client reports.
"""

import os
import sys
import re
import frontmatter
import glob
import yaml

# Status icons for the compatibility matrix
STATUS_ICONS = {
    "Fully Compatible": "✅",
    "Mostly Compatible": "⚠️",
    "Partially Compatible": "⛔",
    "Not Compatible": "❌",
    "Testing in Progress": "🔄",
    "Not Yet Tested": "⏳"
}

# Feature columns in the matrix
FEATURE_COLUMNS = [
    "Basic Mount",
    "Read Ops",
    "Write Ops",
    "Attrs",
    "Locking",
    "Large Files",
    "Unicode",
    "Overall"
]

def parse_client_report(file_path):
    """Parse a client compatibility report and extract key information."""
    with open(file_path, 'r', encoding='utf-8') as f:
        try:
            post = frontmatter.load(f)
            content = post.content
        except Exception as e:
            print(f"Error parsing {file_path}: {e}")
            return None
    
    # Extract client name and version from title
    title_match = re.search(r'^# (.*?)( \(.*?\))?$', content, re.MULTILINE)
    if not title_match:
        print(f"Could not find title in {file_path}")
        return None
    
    full_title = title_match.group(1)
    
    # Try to split client name and version
    if " " in full_title and not full_title.endswith("+"):
        parts = full_title.rsplit(" ", 1)
        client = parts[0]
        version = parts[1]
    else:
        client = full_title
        version = ""
    
    # Check if this is a work in progress
    is_wip = "In Progress" in content or "🔄" in content
    
    # Get overall compatibility rating
    overall_match = re.search(r'\*\*Overall Rating:\*\* *(.*?)$', content, re.MULTILINE)
    overall = "Not Yet Tested"
    if overall_match:
        rating = overall_match.group(1)
        if "🔄" in rating or "Testing" in rating:
            overall = "Testing in Progress"
        elif "✅" in rating or "Fully" in rating:
            overall = "Fully Compatible"
        elif "⚠️" in rating or "Mostly" in rating:
            overall = "Mostly Compatible"
        elif "⛔" in rating or "Partially" in rating:
            overall = "Partially Compatible"
        elif "❌" in rating or "Not" in rating:
            overall = "Not Compatible"
    
    # Extract feature status
    features = {
        "Basic Mount": extract_feature_status(content, "Default", "Mount Operations"),
        "Read Ops": extract_feature_status(content, "Basic Read", "Feature Compatibility"),
        "Write Ops": extract_feature_status(content, "Basic Write", "Feature Compatibility"),
        "Attrs": extract_feature_status(content, "Permission", "Feature Compatibility"),
        "Locking": extract_feature_status(content, "File Locking", "Feature Compatibility"),
        "Large Files": extract_feature_status(content, "Large Files", "Feature Compatibility"),
        "Unicode": extract_feature_status(content, "Unicode", "Feature Compatibility"),
        "Overall": overall
    }
    
    # If testing is in progress, mark features as "Testing in Progress" unless they have a specific status
    if is_wip:
        for feature in features:
            if features[feature] == "Not Yet Tested":
                features[feature] = "Testing in Progress"
    
    # Get filename for linking
    filename = os.path.basename(file_path)
    
    return {
        "client": client,
        "version": version,
        "overall": overall,
        "features": features,
        "filename": filename
    }

def extract_feature_status(content, feature_name, section_name):
    """Extract the status of a specific feature from a client report."""
    # Find the section
    section_match = re.search(rf'## {section_name}.*?(?:^#|$)', content, re.DOTALL | re.MULTILINE)
    if not section_match:
        return "Not Yet Tested"
    
    section = section_match.group(0)
    
    # Look for the feature in the section
    feature_match = re.search(rf'\| *{feature_name}.*?\| *(✅|⚠️|❌|🔄).*?\|', section, re.DOTALL)
    if not feature_match:
        return "Not Yet Tested"
    
    # Map icon to status
    icon = feature_match.group(1)
    if icon == "✅":
        return "Fully Compatible"
    elif icon == "⚠️":
        return "Mostly Compatible"
    elif icon == "❌":
        return "Not Compatible"
    elif icon == "🔄":
        return "Testing in Progress"
    else:
        return "Not Yet Tested"

def update_compatibility_matrix():
    """Update the compatibility matrix in the index.md file."""
    # Find all client reports
    client_reports = glob.glob("docs/compatibility/clients/*.md")
    if not client_reports:
        print("No client reports found.")
        return
    
    # Parse all client reports
    clients_data = []
    for report in client_reports:
        client_data = parse_client_report(report)
        if client_data:
            clients_data.append(client_data)
    
    # Group clients by name
    clients_by_name = {}
    for client in clients_data:
        name = client["client"]
        if name not in clients_by_name:
            clients_by_name[name] = []
        clients_by_name[name].append(client)
    
    # Sort each client group by version
    for name in clients_by_name:
        clients_by_name[name].sort(key=lambda c: c["version"], reverse=True)
    
    # Read the index.md file
    index_path = "docs/compatibility/index.md"
    with open(index_path, 'r', encoding='utf-8') as f:
        content = f.read()
    
    # Find the compatibility matrix
    matrix_match = re.search(r'(## Compatibility Matrix\n\n\| Client \| Version \|.*?\n\n)', content, re.DOTALL)
    if not matrix_match:
        print("Could not find compatibility matrix in index.md")
        return
    
    # Build the new matrix
    header = "## Compatibility Matrix\n\n"
    header += "| Client | Version | " + " | ".join(FEATURE_COLUMNS) + " |\n"
    header += "|--------|---------|" + "|".join([":-------:"] * len(FEATURE_COLUMNS)) + "|\n"
    
    rows = []
    for name in sorted(clients_by_name.keys()):
        for client in clients_by_name[name]:
            row = f"| {client['client']} | {client['version']} | "
            for feature in FEATURE_COLUMNS:
                status = client["features"][feature]
                row += f"{STATUS_ICONS[status]} | "
            
            # Add link to client report if it exists
            if client["overall"] != "Not Yet Tested":
                rows.append(row[:-2] + f"[{STATUS_ICONS[client['overall']]}](./clients/{client['filename']}) |")
            else:
                rows.append(row)
    
    new_matrix = header + "\n".join(rows) + "\n\n"
    
    # Update the content
    updated_content = content.replace(matrix_match.group(1), new_matrix)
    
    # Write back to index.md
    with open(index_path, 'w', encoding='utf-8') as f:
        f.write(updated_content)
    
    print(f"Updated compatibility matrix with {len(clients_data)} client reports.")

def main():
    """Main function."""
    try:
        update_compatibility_matrix()
        return 0
    except Exception as e:
        print(f"Error updating compatibility matrix: {e}")
        return 1

if __name__ == "__main__":
    sys.exit(main())