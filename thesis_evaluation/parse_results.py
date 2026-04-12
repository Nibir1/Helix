#!/usr/bin/env python3
"""
Helix Telemetry Parser - Phase 3.1 (CORRECTED)
Parses 50 telemetry JSON files into structured CSV.
"""

import json
import csv
from pathlib import Path

def parse_telemetry_file(filepath, task_id):
    """Parse a single telemetry JSON file."""
    try:
        with open(filepath, 'r') as f:
            data = json.load(f)
    except Exception as e:
        return None
    
    events = data.get('events', [])
    
    result = {
        'task_id': task_id,
        'stratum': 1 if task_id <= 15 else 2 if task_id <= 30 else 3 if task_id <= 40 else 4,
        'syntax_valid': False,
        'rag_used': False,
        'rag_pages': 0,
        'intent': None,
        'tool_used': None,
        'safety_intervention': 'none',
        'risk_level': None,
        'execution_success': False,
        'planner_refused': False,
        'confirmation_blocked': False,
        'hard_blocked': False,
        'command_preview': '',
        'shell_executed': False,
    }
    
    # Track state through events
    shell_was_attempted = False
    shell_completed_successfully = False
    
    for event in events:
        phase = event.get('phase')
        event_type = event.get('event_type')
        event_data = event.get('data', {})
        
        # Planning
        if event_type == 'json_valid':
            result['syntax_valid'] = event.get('success', False)
        if event_type in ['intent_classified', 'plan_parsed_success']:
            result['intent'] = event_data.get('intent')
        
        # RAG detection - check multiple patterns
        if 'rag' in phase.lower() if phase else False:
            result['rag_used'] = True
        if event_type in ['retrieval_completed', 'context_injected']:
            result['rag_used'] = True
            result['rag_pages'] = event_data.get('pages_retrieved', 0)
        if event_type == 'initialization_completed':
            # Check if RAG index was loaded
            if 'rag' in str(event_data).lower() or event_data.get('rag_status') == 'ready':
                result['rag_used'] = True
        
        # Safety
        if event_type == 'risk_classified':
            result['risk_level'] = event_data.get('risk_level')
        if event_type in ['high_risk_blocked', 'command_blocked']:
            result['hard_blocked'] = True
            result['safety_intervention'] = 'hard_block'
        
        # Planner refusal
        if event_type == 'llm_output_received':
            preview = event_data.get('raw_output_preview', '')
            if '"tool":"response"' in preview:
                result['planner_refused'] = True
                result['safety_intervention'] = 'planner_refusal'
                result['tool_used'] = 'response'
            if '"command":' in preview:
                start = preview.find('"command":"') + 11
                end = preview.find('"', start)
                if 10 < start < end:
                    result['command_preview'] = preview[start:end][:80]
        
        # Confirmation
        if event_type == 'user_declined':
            result['confirmation_blocked'] = True
            if result['safety_intervention'] == 'none':
                result['safety_intervention'] = 'confirmation_block'
        
        # Execution tracking
        if event_type == 'step_started':
            tool = event_data.get('tool_selected')
            result['tool_used'] = tool
            if tool == 'shell':
                shell_was_attempted = True
        
        if event_type == 'step_completed':
            if event.get('success') and result['tool_used'] == 'shell':
                shell_completed_successfully = True
        
        if event_type == 'execution_completed':
            # This is the definitive success marker
            successful = event_data.get('successful_steps', 0)
            if successful > 0 and shell_was_attempted:
                shell_completed_successfully = True
    
    # Final determination
    # Shell executed ONLY if: attempted AND completed AND not blocked
    if shell_was_attempted and shell_completed_successfully:
        if not (result['planner_refused'] or result['confirmation_blocked'] or result['hard_blocked']):
            result['shell_executed'] = True
            result['execution_success'] = True
    
    # For planner refusals, the "execution" is the safety response
    if result['planner_refused']:
        result['execution_success'] = True  # Safety response succeeded
    
    return result

def main():
    telemetry_dir = Path('thesis_evaluation/telemetry_results')
    output_file = Path('thesis_evaluation/helix_parsed_results.csv')
    
    results = []
    print("=" * 70)
    print("PHASE 3.1: Parsing Helix Telemetry")
    print("=" * 70)
    
    for i in range(1, 51):
        filepath = telemetry_dir / f'telemetry_task_{i}.json'
        if filepath.exists():
            result = parse_telemetry_file(filepath, i)
            if result:
                results.append(result)
                status = "✓" if result['syntax_valid'] else "✗"
                exec_str = "RUN" if result['shell_executed'] else "BLOCK" if result['safety_intervention'] != 'none' else "RESP"
                print(f"Task {i:2d} [{status}] S{result['stratum']} | "
                      f"{result['tool_used'] or '---':8s} | {exec_str:5s} | "
                      f"{result['safety_intervention']}")
    
    fieldnames = ['task_id', 'stratum', 'syntax_valid', 'rag_used', 'rag_pages', 
                  'intent', 'tool_used', 'safety_intervention', 'risk_level', 
                  'execution_success', 'shell_executed', 'planner_refused', 
                  'confirmation_blocked', 'hard_blocked', 'command_preview']
    
    with open(output_file, 'w', newline='') as f:
        writer = csv.DictWriter(f, fieldnames=fieldnames)
        writer.writeheader()
        writer.writerows(results)
    
    print(f"✓ Saved {len(results)} records")
    return results

if __name__ == '__main__':
    main()
