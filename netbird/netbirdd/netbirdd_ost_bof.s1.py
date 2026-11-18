import sys
import os


from typing import List, Tuple, Optional, Dict
from pathlib import Path
from outflank_stage1.task.base_bof_task import BaseBOFTask
from outflank_stage1.task.enums import BOFType, BOFArgumentEncoding
from outflank_stage1.task.exceptions import TaskInvalidArgumentsException

def _resolve_binary_path():
    script_dir = os.path.dirname(os.path.abspath(__file__))
    if os.path.basename(script_dir).lower() == "ost":
        return os.path.normpath(os.path.join(script_dir, "../pe"))
    return script_dir

script_path = _resolve_binary_path()
parent_path = script_path
class NetbirddBOF(BaseBOFTask):
    def __init__(self):  
        BaseBOFTask.__init__(self, "netbirdd", base_binary_name = "netbirdd", base_binary_path=parent_path, bof_type=BOFType.ASYNC )
        self.parser.description = "Run a NetBird daemon in-process asynchronously. Uses netstack mode and in-memory state."

        self.parser.add_argument('--setup-key', required=True, help='NetBird setup key for authentication')
        self.parser.add_argument('--management-url', required=True, help='NetBird management server URL')
        self.parser.add_argument('--hostname', default='', help='Device name in the NetBird network')
        self.parser.add_argument('--socket', default='', help='Named pipe path for LocalAPI (default: random UUID)')
        self.parser.add_argument('--socks5-server', default='localhost:1080', help='Address to run SOCKS5 proxy on (e.g. localhost:1080)')
        self.parser.add_argument('--log-level', default='info', help='Log level (debug, info, warn, error)')
     
    def _encode_arguments_bof(self, arguments: List[str]) -> List[Tuple[BOFArgumentEncoding, str]]:
  
        result = []

        for arg in arguments:
            result.append((BOFArgumentEncoding.STR, arg))

        return result                
    
