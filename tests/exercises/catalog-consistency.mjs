#!/usr/bin/env node
import { readdir, readFile } from 'node:fs/promises';
import { join } from 'node:path';

const root = new URL('../../', import.meta.url);
const scenarioRoot = new URL('scenarios/', root);
const frontendPath = new URL('frontend/src/content/exercises.json', root);
const scenarioDirs = (await readdir(scenarioRoot, { withFileTypes: true }))
  .filter(entry => entry.isDirectory()).map(entry => entry.name).sort();
const exercises = JSON.parse(await readFile(frontendPath, 'utf8'));
const frontendIDs = exercises.filter(item => item.status === 'available' && item.id !== 'first-investigation').map(item => item.id).sort();
const scenarioIDs = scenarioDirs.filter(id => id !== 'first-investigation').sort();
if (JSON.stringify(frontendIDs) !== JSON.stringify(scenarioIDs)) {
  throw new Error(`catalog mismatch: frontend=${frontendIDs.join(',')} scenarios=${scenarioIDs.join(',')}`);
}
for (const id of scenarioIDs) {
  const yaml = await readFile(new URL(`${id}/scenario.yaml`, scenarioRoot), 'utf8');
  if (!/^goal:\s*\S/m.test(yaml) || !/^success_criteria:\s*\[/m.test(yaml) || !/^prerequisites:\s*\[/m.test(yaml) || !/^required_tools:\s*\[/m.test(yaml) || !/^false_hypotheses:\s*\[/m.test(yaml)) throw new Error(`${id}: missing exercise metadata`);
}
console.log(`catalog consistent: ${scenarioIDs.length} executable scenarios`);
