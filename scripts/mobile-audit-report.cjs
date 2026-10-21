const fs = require("node:fs");
const https = require("node:https");

function escapeMarkdown(value) {
  return String(value).replace(/\|/g, "\\|");
}

function appendSummary(markdown) {
  const summaryPath = process.env.GITHUB_STEP_SUMMARY;
  if (summaryPath) {
    fs.appendFileSync(summaryPath, markdown);
  }
}

function notify(webhookUrl, message) {
  return new Promise((resolve) => {
    let endpoint;
    try {
      endpoint = new URL(webhookUrl);
      if (endpoint.protocol !== "https:" || endpoint.username || endpoint.password) {
        console.error("Chat notification skipped: WEBHOOK_URL must be an HTTPS URL without credentials.");
        resolve();
        return;
      }
    } catch {
      console.error("Chat notification skipped: WEBHOOK_URL is invalid.");
      resolve();
      return;
    }

    const request = https.request(endpoint, {
      method: "POST",
      headers: { "content-type": "application/json" },
    }, (response) => {
      response.resume();
      if (response.statusCode < 200 || response.statusCode >= 300) {
        console.error(`Chat notification failed with HTTP ${response.statusCode}.`);
      }
      resolve();
    });

    request.setTimeout(5000, () => request.destroy(new Error("request timed out")));
    request.on("error", (error) => {
      console.error(`Chat notification failed: ${error.message}`);
      resolve();
    });
    request.end(JSON.stringify({ text: message }));
  });
}

async function main() {
  const reportPath = process.argv[2];
  if (!reportPath) {
    throw new Error("Usage: node mobile-audit-report.cjs <audit-json-path>");
  }

  const auditExitCode = Number(process.env.AUDIT_EXIT_CODE);
  let audit;
  let reportError = false;
  try {
    audit = JSON.parse(fs.readFileSync(reportPath, "utf8"));
  } catch {
    reportError = true;
  }

  const validVulnerabilityReport = audit?.vulnerabilities
    && typeof audit.vulnerabilities === "object"
    && !Array.isArray(audit.vulnerabilities);
  reportError ||= !audit || Boolean(audit.error) || !validVulnerabilityReport;
  const vulnerabilities = validVulnerabilityReport ? Object.entries(audit.vulnerabilities) : [];
  const lines = [
    "### Mobile dependency audit",
    "",
    reportError
      ? "Audit could not produce a valid report; inspect the job logs."
      : `Findings: **${vulnerabilities.length} vulnerable package${vulnerabilities.length === 1 ? "" : "s"}**.`,
    "",
  ];

  if (!reportError && vulnerabilities.length > 0) {
    lines.push("| Package | Severity | Direct |");
    lines.push("| --- | --- | --- |");
    for (const [name, finding] of vulnerabilities.slice(0, 100)) {
      const severity = /^[a-z]+$/i.test(finding.severity || "") ? finding.severity.toUpperCase() : "UNKNOWN";
      lines.push(`| \`${escapeMarkdown(name)}\` | ${severity} | ${finding.isDirect ? "Yes" : "No"} |`);
    }
    if (vulnerabilities.length > 100) {
      lines.push("", `Showing the first 100 of ${vulnerabilities.length} affected packages.`);
    }
  } else if (!reportError) {
    lines.push("No vulnerable packages reported.");
  }

  const failed = reportError || !Number.isInteger(auditExitCode) || auditExitCode !== 0 || vulnerabilities.length > 0;
  if (failed) {
    lines.push("", "**Result: failed. Dependency audit findings remain blocking.**");
  } else {
    lines.push("", "**Result: passed.**");
  }
  appendSummary(`${lines.join("\n")}\n`);

  if (failed && process.env.GITHUB_EVENT_NAME === "push" && process.env.WEBHOOK_URL) {
    const repository = process.env.GITHUB_REPOSITORY || "unknown repository";
    const runUrl = `https://github.com/${repository}/actions/runs/${process.env.GITHUB_RUN_ID}`;
    const packages = vulnerabilities.slice(0, 30)
      .map(([name, finding]) => `${name} (${finding.severity || "unknown"})`)
      .join(", ");
    const more = vulnerabilities.length > 30 ? `, and ${vulnerabilities.length - 30} more` : "";
    const reportStatus = reportError ? " Audit report unavailable; inspect the job logs." : "";
    await notify(
      process.env.WEBHOOK_URL,
      `Mobile dependency audit failed for ${repository}: ${vulnerabilities.length} vulnerable packages. ${packages}${more}.${reportStatus} Workflow: ${runUrl}`,
    );
  }

  if (failed) {
    throw new Error(`Mobile dependency audit failed with exit code ${process.env.AUDIT_EXIT_CODE || "unknown"}.`);
  }
}

main().catch((error) => {
  console.error(error.message);
  process.exitCode = 1;
});
