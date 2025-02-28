#!/usr/bin/env python3
"""
Validate client compatibility reports to ensure they follow the required format and contain all necessary information.
"""

import os
import sys
import re
import frontmatter
import glob

# Required sections in client reports
REQUIRED_SECTIONS = [
    "Compatibility Summary",
    "Mount Operations",
    "Feature Compatibility",
    "Performance Metrics",
    "Test Environment Details",
    "Test Cases Executed"
]

# Required mount options to document
REQUIRED_MOUNT_OPTIONS = [
    "Default (no options)",
    "-o ro",
    "-o rw"
]

# Required features to document
REQUIRED_FEATURES = [
    "Basic Read",
    "Basic Write",
    "File Creation",
    "File Deletion",
    "Directory Creation",
    "Directory Listing",
    "Large Files (>2GB)",
    "Unicode Filenames"
]

def validate_client_report(file_path):
    """Validate a client compatibility report file."""
    print(f"Validating {file_path}...")
    
    with open(file_path, 'r', encoding='utf-8') as f:
        try:
            # Parse frontmatter
            post = frontmatter.load(f)
            content = post.content
        except Exception as e:
            print(f"  ERROR: Failed to parse frontmatter: {e}")
            return False
    
    # Check for required sections
    missing_sections = []
    for section in REQUIRED_SECTIONS:
        if not re.search(r'^## ' + re.escape(section), content, re.MULTILINE):
            missing_sections.append(section)
    
    if missing_sections:
        print(f"  ERROR: Missing required sections: {', '.join(missing_sections)}")
        return False
    
    # Check for mount options table
    mount_table_match = re.search(r'## Mount Operations.*?\|(.*?)\n((?:\|.*\n)+)', content, re.DOTALL)
    if not mount_table_match:
        print("  ERROR: Mount operations table not found or not properly formatted")
        return False
    
    # Check mount options
    mount_table = mount_table_match.group(2)
    missing_options = []
    for option in REQUIRED_MOUNT_OPTIONS:
        if not re.search(r'\| *' + re.escape(option) + r' *\|', mount_table):
            missing_options.append(option)
    
    if missing_options:
        print(f"  WARNING: Missing documentation for mount options: {', '.join(missing_options)}")
    
    # Check feature compatibility table
    feature_table_match = re.search(r'## Feature Compatibility.*?\|(.*?)\n((?:\|.*\n)+)', content, re.DOTALL)
    if not feature_table_match:
        print("  ERROR: Feature compatibility table not found or not properly formatted")
        return False
    
    # Check features
    feature_table = feature_table_match.group(2)
    missing_features = []
    for feature in REQUIRED_FEATURES:
        if not re.search(r'\| *' + re.escape(feature) + r' *\|', feature_table):
            missing_features.append(feature)
    
    if missing_features:
        print(f"  WARNING: Missing documentation for features: {', '.join(missing_features)}")
    
    # Check performance metrics section
    perf_table_match = re.search(r'## Performance Metrics.*?\|(.*?)\n((?:\|.*\n)+)', content, re.DOTALL)
    if not perf_table_match:
        print("  WARNING: Performance metrics table not found or not properly formatted")
    
    # Check test environment details
    if not re.search(r'## Test Environment Details.*?Client Hardware:', content, re.DOTALL):
        print("  WARNING: Test environment details section appears incomplete")
    
    # Check test cases
    test_cases_match = re.search(r'## Test Cases Executed(.*?)(?:^#|$)', content, re.DOTALL | re.MULTILINE)
    if not test_cases_match or not re.search(r'- \[([ x])\]', test_cases_match.group(1)):
        print("  WARNING: Test cases section should include a checklist")
    
    print("  Validation complete.")
    return True

def main():
    """Main function to validate all client reports."""
    client_reports = glob.glob("docs/compatibility/clients/*.md")
    
    if not client_reports:
        print("No client reports found to validate.")
        return 0
    
    success = True
    for report in client_reports:
        result = validate_client_report(report)
        success = success and result
    
    if success:
        print("\nAll client reports passed validation!")
        return 0
    else:
        print("\nSome client reports have validation errors. Please fix them before committing.")
        return 1

if __name__ == "__main__":
    sys.exit(main())