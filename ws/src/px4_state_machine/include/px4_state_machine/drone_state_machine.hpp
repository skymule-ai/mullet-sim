#ifndef DRONE_STATE_MACHINE_HPP
#define DRONE_STATE_MACHINE_HPP

#include <rclcpp/rclcpp.hpp>
// #include "px4_interface.hpp"

class DroneStateMachine : public rclcpp::Node
{
public:
    DroneStateMachine() : Node("drone_state_machine"), current_state_(State::IDLE) {
        this->declare_parameter<int>("drone_id", 1);
        this->get_parameter("drone_id", drone_id_);
    }

    void takeOff();
    void hold();
    void land();
    void shutdown();

private:
    enum class State
    {
        /*
        0: IDLE - Waiting for takeoff command
        1: TAKING_OFF - Executing takeoff procedure
        2: HOLDING - Maintaining position
        3: LANDING - Executing landing procedure
        4: MISSION - Performing mission tasks (e.g., waypoint navigation)
        */
        IDLE,
        TAKING_OFF,
        HOLDING,
        LANDING,
        MISSION
    };

    int drone_id_;

    State current_state_;

    void transitionTo(State new_state);
};

#endif // DRONE_STATE_MACHINE_HPP