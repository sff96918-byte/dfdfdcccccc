#!/usr/bin/env node
'use strict';
// Contract checker CLI.
//
//   node kit/check.cjs --site chitchat
//   node kit/check.cjs --site example-room
//   node kit/check.cjs --all
//   node kit/check.cjs --observer sites/mysite/observer.js --page saved-page.html --url https://mysite.example/
//
// Exit code 0 only when no rule FAILs. SKIP is reported and is never counted as
// a pass (see README "Verification").

const fs = require('node:fs');
const path = require('node:path');

const {siteRegistry, siteTargets, check, ROOT} = require('./run.cjs');
const {summarize} = require('./contract.cjs');

function parseArgs(argv) {
  const args = {site: '', all: false, observer: '', page: '', url: '', foreign: ''};
  for (let i = 0; i < argv.length; i += 1) {
    const token = argv[i];
    const next = () => {
      i += 1;
      if (i >= argv.length) throw new Error('missing value for ' + token);
      return argv[i];
    };
    if (token === '--site') args.site = next();
    else if (token === '--all') args.all = true;
    else if (token === '--observer') args.observer = next();
    else if (token === '--page') args.page = next();
    else if (token === '--url') args.url = next();
    else if (token === '--foreign') args.foreign = next();
    else if (token === '-h' || token === '--help') args.help = true;
    else throw new Error('unknown argument: ' + token);
  }
  return args;
}

function usage() {
  process.stdout.write(
    [
      'Site-adapter contract checker',
      '',
      '  node kit/check.cjs --site <name>            check a site from sites.json',
      '  node kit/check.cjs --all                    check every registered site',
      '  node kit/check.cjs --observer <js> --page <html> [--url <url>] [--foreign <html>]',
      '',
      'Registered sites: ' + Object.keys(siteRegistry()).join(', '),
      'This kit never opens a browser and never touches the network.',
      ''
    ].join('\n')
  );
}

function report(result) {
  process.stdout.write('\n' + result.name.toUpperCase() + '  (' + result.target.kind + (result.target.status ? ' - ' + result.target.status : '') + ')\n');
  process.stdout.write('  page:     ' + path.relative(ROOT, result.target.pagePath) + '\n');
  process.stdout.write('  observer: ' + path.relative(ROOT, result.target.observerPath) + '\n');
  if (result.target.foreignPagePath && fs.existsSync(result.target.foreignPagePath)) {
    process.stdout.write('  foreign:  ' + path.relative(ROOT, result.target.foreignPagePath) + '\n');
  }
  process.stdout.write('  probe:    ' + (result.probe || '(empty)') + '\n\n');
  for (const row of result.rows) {
    process.stdout.write('  [' + row.status.padEnd(4) + '] ' + row.id + ' - ' + row.detail + '\n');
  }
  const summary = result.summary;
  process.stdout.write('  => PASS ' + summary.pass + ', FAIL ' + summary.fail + ', SKIP ' + summary.skip + '\n');
}

function runTargets(targets) {
  const results = [];
  for (const target of targets) {
    const result = check(target);
    result.target = target;
    report(result);
    results.push(result);
  }
  const all = results.flatMap((r) => r.rows);
  const summary = summarize(all);
  process.stdout.write('\nTOTAL: PASS ' + summary.pass + ', FAIL ' + summary.fail + ', SKIP ' + summary.skip + '\n');
  if (summary.fail) {
    process.stdout.write('RESULT: FAIL - the adapter would misbehave on this page.\n');
    process.exitCode = 1;
  } else if (summary.skip) {
    process.stdout.write('RESULT: PASS with ' + summary.skip + ' SKIP rule(s) - SKIP is not a pass; supply the missing input to remove it.\n');
  } else {
    process.stdout.write('RESULT: PASS - every contract rule was checked on the provided page.\n');
  }
  return results;
}

function main() {
  let args;
  try {
    args = parseArgs(process.argv.slice(2));
  } catch (error) {
    process.stderr.write(error.message + '\n');
    process.exitCode = 2;
    return;
  }
  if (args.help || (!args.all && !args.site && !args.observer)) {
    usage();
    return;
  }
  try {
    let targets = [];
    if (args.observer) {
      const page = args.page ? path.resolve(args.page) : '';
      if (!page || !fs.existsSync(page)) throw new Error('--page <html> is required together with --observer');
      targets = [
        {
          name: path.basename(args.observer, '.js'),
          url: args.url || 'https://example.invalid/',
          observerPath: path.resolve(args.observer),
          pagePath: page,
          foreignPagePath: args.foreign ? path.resolve(args.foreign) : '',
          kind: 'dom',
          status: 'ad-hoc'
        }
      ];
    } else if (args.all) {
      targets = Object.keys(siteRegistry()).map((name) => siteTargets(name));
    } else {
      targets = [siteTargets(args.site)];
    }
    runTargets(targets);
  } catch (error) {
    process.stderr.write('ERROR: ' + error.message + '\n');
    process.exitCode = 2;
  }
}

main();
