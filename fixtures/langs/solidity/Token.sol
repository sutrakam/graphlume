// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

import {IERC20} from "./IERC20.sol";

type Price is uint256;
type Id is bytes32;

error Unauthorized(address caller);
error TooLate();

event Registered(address indexed who);

uint256 constant MAX_SUPPLY = 1_000_000;
address constant ZERO = address(0);

enum Status { Active, Paused, Closed }

struct Account {
    uint256 balance;
    uint64 since;
}

function freeAdd(uint256 a, uint256 b) pure returns (uint256) {
    return a + b;
}

function freeSub(uint256 a, uint256 b) pure returns (uint256) {
    uint256 local = a - b;
    return local;
}

library MathLib {
    uint256 internal constant SCALE = 1e18;

    function mulScaled(uint256 a, uint256 b) internal pure returns (uint256) {
        return a * b / SCALE;
    }
}

interface IVault {
    event Deposited(address indexed from, uint256 amount);
    error VaultClosed();

    function deposit(uint256 amount) external;

    function balanceOf(address who) external view returns (uint256);
}

abstract contract Base {
    function name() public view virtual returns (string memory);
}

contract Token is Base, IVault {
    using MathLib for uint256;

    bytes32 public constant ADMIN_ROLE = keccak256("ADMIN_ROLE");
    uint8 private constant DECIMALS = 18;
    address public immutable owner;
    uint256 public totalSupply;
    mapping(address => uint256) private balances;
    string private _name = "Token";
    Status public status;

    struct Checkpoint {
        uint32 fromBlock;
        uint224 votes;
    }

    enum Mode { Open, Locked }

    event Transfer(address indexed from, address indexed to, uint256 value);
    error InsufficientBalance(uint256 available, uint256 needed);

    modifier onlyOwner() {
        require(msg.sender == owner, "owner");
        _;
    }

    modifier whenActive {
        require(status == Status.Active);
        _;
    }

    constructor(address initialOwner) {
        owner = initialOwner;
    }

    receive() external payable {}

    fallback() external payable {}

    function name() public view override returns (string memory) {
        return _name;
    }

    function transfer(address to, uint256 value) public onlyOwner returns (bool) {
        uint256 fee = value / 100;
        balances[to] += value - fee;
        emit Transfer(msg.sender, to, value);
        return true;
    }

    function deposit(uint256 amount) external override {
        uint256 localCopy = amount;
        totalSupply += localCopy;
    }

    function balanceOf(address who) external view override returns (uint256) {
        return balances[who];
    }

    function _burn(
        address from,
        uint256 amount
    ) internal virtual {
        balances[from] -= amount;
    }
}
