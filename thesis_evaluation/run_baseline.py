# thesis_evaluation/run_baseline.py

import csv
import subprocess
import os
import time
from openai import OpenAI

# Initialize OpenAI client (Baseline Model)
client = OpenAI()

# Dynamic pathing: Allows script to be run from root or from within the folder
SCRIPT_DIR = os.path.dirname(os.path.abspath(__file__))
DATASET_FILE = os.path.join(SCRIPT_DIR, "dataset.csv")
OUTPUT_FILE = os.path.join(SCRIPT_DIR, "baseline_results.csv")

# The strictly unconstrained baseline prompt
SYSTEM_PROMPT = """
You are a CLI automation script.
Output ONLY the exact bash command for the requested task.
Do NOT use markdown code blocks (no ```).
Do NOT explain your answer.
Do NOT include conversational text.
Just output the raw string to be executed.
"""

def execute_in_docker(command):
    """Executes the raw command in a sterile, ephemeral Ubuntu container."""
    docker_cmd = [
        "docker", "run", "--rm", "ubuntu:24.04", 
        "bash", "-c", command
    ]
    
    try:
        # Run the container with a 15-second timeout
        result = subprocess.run(
            docker_cmd, 
            capture_output=True, 
            text=True, 
            timeout=15
        )
        return result.returncode, result.stdout, result.stderr
    except subprocess.TimeoutExpired:
        return 124, "", "Command timed out."
    except Exception as e:
        return -1, "", str(e)

def main():
    print("🚀 Starting Baseline (Control Group) Execution...")
    
    if not os.path.exists(DATASET_FILE):
        print(f"❌ Error: Could not find {DATASET_FILE}")
        return

    with open(DATASET_FILE, 'r', encoding='utf-8') as infile, \
         open(OUTPUT_FILE, 'w', newline='', encoding='utf-8') as outfile:
        
        reader = csv.DictReader(infile, delimiter='|')
        writer = csv.writer(outfile, delimiter='|')
        
        # Write headers for the output CSV
        writer.writerow(["TaskID", "Stratum", "Expected_Tool", "Generated_Command", "Exit_Code", "Syntax_Valid"])
        
        for row in reader:
            task_id = row['TaskID']
            stratum = row['Stratum']
            prompt = row['Prompt']
            tool = row['Expected_Tool']
            
            print(f"▶️  [Task {task_id}/50] Stratum {stratum}...")
            
            # 1. Get raw LLM generation
            try:
                response = client.chat.completions.create(
                    model="gpt-4o",
                    messages=[
                        {"role": "system", "content": SYSTEM_PROMPT},
                        {"role": "user", "content": f"Task: {prompt}"}
                    ],
                    temperature=0.7,
                    max_tokens=150
                )
                generated_command = response.choices[0].message.content.strip()
            except Exception as e:
                print(f"   ❌ API Error: {e}")
                writer.writerow([task_id, stratum, tool, "API_ERROR", -1, 0])
                continue

            # Check if LLM disobeyed and included markdown or conversational text
            syntax_valid = 1
            if "```" in generated_command or "\n" in generated_command:
                syntax_valid = 0
                print("   ⚠️  Warning: Model generated invalid syntax (markdown/multiline).")

            # 2. Execute directly in Docker
            print(f"   ⚙️  Executing: {generated_command[:50]}...")
            exit_code, stdout, stderr = execute_in_docker(generated_command)
            
            # 3. Log results
            if exit_code == 0:
                print(f"   ✅ Success (Exit Code 0)")
            else:
                print(f"   ❌ Failed (Exit Code {exit_code})")
            
            writer.writerow([task_id, stratum, tool, generated_command, exit_code, syntax_valid])
            time.sleep(1)

    print(f"\n🎉 Baseline execution complete. Results saved to {OUTPUT_FILE}")

if __name__ == "__main__":
    main()