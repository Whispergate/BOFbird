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

class NetbirdBOF(BaseBOFTask):
    def __init__(self):  
        BaseBOFTask.__init__(self, "netbird", base_binary_name = "netbird", base_binary_path=parent_path)
        self.parser.description = "Control a NetBird daemon running on host via the in-memory netbirdd BOF."
        
        # Global flags
        self.parser.add_argument('--socket', metavar='PATH',
                        help='path to netbirdd named pipe')
        
        # Create subparsers
        subparsers = self.parser.add_subparsers(dest='command', help='subcommand help')
        
        # up command
        up_parser = subparsers.add_parser('up', help='Start/restart NetBird connection')

        # down command
        down_parser = subparsers.add_parser('down', help='Stop NetBird connection')
                
        # status command
        status_parser = subparsers.add_parser('status',
                                            help='Get connection status')
        status_parser.add_argument('--json', action='store_true',
                                help='output in JSON format')
        
        # shutdown command
        shutdown_parser = subparsers.add_parser('shutdown', help='Shutdown the running netbirdd instance')
         

    def _encode_arguments_bof(self, arguments: List[str]) -> List[Tuple[BOFArgumentEncoding, str]]:

        if not arguments:
            raise TaskInvalidArgumentsException("No args provided")

        #Just parse the arguments to make sure they are valid
        parser_arguments = self.parser.parse_args(arguments)
        
        #Now just pack all the args as provided to the BOF itself 
        result = []
        for arg in arguments:
            result.append((BOFArgumentEncoding.STR, arg))

        return result                
    
