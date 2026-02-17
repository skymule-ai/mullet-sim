#include "px4_state_machine/drone_state_machine.hpp"
// #include "px4_state_machine/px4_interface.hpp"
#include <rclcpp/rclcpp.hpp>

DroneStateMachine::DroneStateMachine()
    : Node("drone_state_machine"), current_state(State::IDLE)
{
    px4_interface_ = std::make_shared<Px4Interface>();

    // Initialize publishers and subscribers
    // command_publisher_ = this->create_publisher<VehicleCommand>("/fmu/in/vehicle_command", 10);
    state_subscriber_ = this->create_subscription<VehicleState>(
        "/vehicle_state", 10,
        std::bind(&DroneStateMachine::state_callback, this, std::placeholders::_1));

    // Timer for state machine execution
    timer_ = this->create_wall_timer(100ms, std::bind(&DroneStateMachine::execute_state, this));
}

void DroneStateMachine::state_callback(const VehicleState::SharedPtr msg)
{
    // Update current state based on vehicle state
    // Logic to handle state updates
}

void DroneStateMachine::execute_state()
{
    switch (current_state)
    {
    case State::IDLE:
        // Wait for command to take off
        break;
    case State::TAKEOFF:
        takeoff();
        break;
    case State::HOLD:
        hold();
        break;
    case State::LAND:
        land();
        break;
    }
}

void DroneStateMachine::takeoff()
{
    // Logic to send takeoff command
    // px4_interface_->takeoff();
    // current_state = State::HOLD; // Transition to hold after takeoff
}

void DroneStateMachine::hold()
{
    // Logic to maintain position
}

void DroneStateMachine::land()
{
    // Logic to send land command
    // px4_interface_->land();
    current_state = State::IDLE; // Transition back to idle after landing
}

void DroneStateMachine::shutdown()
{
    land(); // Ensure the drone lands before shutdown
}

int main(int argc, char *argv[])
{
    rclcpp::init(argc, argv);
    auto drone_state_machine = std::make_shared<DroneStateMachine>();
    rclcpp::spin(drone_state_machine);
    rclcpp::shutdown();
    return 0;
}