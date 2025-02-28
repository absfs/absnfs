# ABSNFS Client Compatibility Testing Project

This file provides instructions for setting up and managing the GitHub project for ABSNFS client compatibility testing.

## Setting up the GitHub Project

1. Go to your GitHub repository (https://github.com/absfs/absnfs)
2. Navigate to the "Projects" tab
3. Click "New project"
4. Select "Board" as the template
5. Name the project "NFS Client Compatibility Testing"
6. Create the project

## Configuring Project Columns

Configure the project with the following columns:

1. **📋 Backlog**: Clients identified but not yet scheduled for testing
2. **🔍 Researching**: Active research on client behavior and test environment setup
3. **🧪 Testing**: Active testing of client compatibility
4. **📝 Documenting**: Writing up test results and documentation
5. **✅ Complete**: Testing and documentation complete

## Setting up Automation

To automate project card movement, create the following automation rules:

1. Move issues with label "client-research" to "Researching" when added to project
2. Move issues with label "client-testing" to "Testing" when added to project
3. Move issues with label "client-documentation" to "Documenting" when added to project
4. Move issues with label "client-complete" to "Complete" when added to project

## Creating Client Issue Template

Create a new issue template at `.github/ISSUE_TEMPLATE/client-compatibility.md` with the following content:

```markdown
---
name: Client Compatibility Testing
about: Track compatibility testing for a specific NFS client
title: 'Client Compatibility: [CLIENT NAME] [VERSION]'
labels: compatibility, client-research
assignees: ''
---

## Client Information
- **Client OS/Name:** 
- **Version:** 
- **Environment:** 

## Testing Status
- [ ] Research client behavior and requirements
- [ ] Set up test environment
- [ ] Execute basic mount tests
- [ ] Test file operations
- [ ] Test directory operations
- [ ] Test attribute handling
- [ ] Test special cases
- [ ] Test error handling
- [ ] Benchmark performance
- [ ] Document findings
- [ ] Create compatibility report
- [ ] Update compatibility matrix

## Notes
Add any preliminary information or notes about this client here.

## Resources
Links to relevant documentation or resources for this client.
```

## Creating Client Cards

For each client in the testing queue, create a project card:

1. Create a new issue using the "Client Compatibility Testing" template
2. Fill in the client details
3. The issue will automatically be added to the project in the "Researching" column
4. Update the issue labels as testing progresses to move it through the columns

## Tracking Progress

Use the project board to track overall progress:

1. Use the "Backlog" column to prioritize clients
2. Move clients through the workflow as testing progresses
3. Use the issue checklist to track detailed progress for each client
4. Update labels to trigger automation

## Reporting

Generate regular progress reports based on the project board:

1. Count cards in each column for overall status reporting
2. Use issue checklists to calculate detailed completion percentages
3. Reference the progress in weekly reports

## Integration with Documentation

The GitHub project should be integrated with the documentation workflow:

1. When an issue moves to "Complete", ensure the compatibility report is finalized
2. Update the compatibility matrix in the documentation
3. Link to the completed issue from the client report for reference

## Project Review Schedule

Schedule regular project reviews:

1. Weekly review of active testing (cards in "Testing" column)
2. Bi-weekly review of overall project progress
3. Monthly planning for upcoming client testing

---

This project board provides a visual representation of the compatibility testing progress and helps coordinate the work across team members. It complements the detailed documentation tracking in the `docs/compatibility/` directory.